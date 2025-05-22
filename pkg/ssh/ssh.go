package ssh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type FileStat interface {
	IsDir() bool
	IsRegular() bool
}

type SshSession interface {
	Stat(path string) (FileStat, error)
	SendDir(progress io.Writer, src, dst string) error
	SendFile(progress io.Writer, src, dst string) error
	Exec(cmdOutput io.Writer, cmd string, param ...string) error
	ReceiveRemoteFile(progress io.Writer, remoteSrc, dst string) error
	ReceiveRemoteDir(progress io.Writer, remoteDir, dst string) error
	RemoveFile(path string) error
	Mkdir(path string) error
	RemoveDirectory(path string) error
	RemoveAll(srcdir string) error
	SetSftpConcurrency(concurrency bool)
	Close()
}

const knownhostFile = ".known_hosts"

var knownHostKeyCallback ssh.HostKeyCallback

type sftpFunc func(progress *progressCopy, src, dest string) error

type sshSession struct {
	sftp            *sftpclient
	client          *ssh.Client
	trustServerHost bool
}

func (s *sshSession) Close() {
	s.client.Close()
	s.sftp.Close()
}

func (s *sshSession) serverHostKey(hostname string, remote net.Addr, key ssh.PublicKey) error {
	err := knownHostKeyCallback(hostname, remote, key)
	if err != nil && s.trustServerHost {
		file, err := os.OpenFile(knownhostFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open known hosts file")
		}
		defer file.Close()

		line := knownhosts.Line([]string{hostname}, key)
		_, err = file.WriteString(line + "\n")
		if err != nil {
			return fmt.Errorf("failed to write to known hosts file")
		}
		return nil
	} else if err != nil && !s.trustServerHost {
		return err
	}
	return nil
}

func (s *sshSession) Exec(cmdOutput io.Writer, command string, args ...string) error {
	session, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session")
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create session")
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create session")
	}
	done := make(chan bool, 2)
	defer func() {
		<-done
		<-done
		close(done)
	}()
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			cmdOutput.Write(scanner.Bytes())
		}
		done <- true
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			cmdOutput.Write(scanner.Bytes())
		}
		done <- true
	}()
	if err := session.Start(command + " " + strings.Join(args, " ")); err != nil {
		return err
	}
	if err := session.Wait(); err != nil {
		exitError, ok := err.(*ssh.ExitError)
		if ok {
			return fmt.Errorf("command exited with status: %d : %s", exitError.ExitStatus(), exitError.Msg())
		} else {
			return err
		}
	}
	return nil
}

func remoterealpath(s *sshSession, dest string) (string, error) {
	if dest == "" || dest == "~" || dest == "~/" {
		dest = "."
	} else if dest[:2] == "~/" {
		dest = path.Join(".", dest[2:])
	}
	dest, err := s.sftp.RealPath(dest)
	if err != nil {
		return "", err
	}
	return dest, nil
}

func (s *sshSession) Stat(srcpath string) (FileStat, error) {
	srcpath, err := remoterealpath(s, srcpath)
	if err != nil {
		return nil, err
	}
	return s.sftp.Stat(srcpath)
}

func (s *sshSession) Mkdir(dirPath string) error {
	dirPath, err := remoterealpath(s, dirPath)
	if err != nil {
		return err
	}
	return s.sftp.Mkdir(dirPath)
}

func (s *sshSession) sendfile(progress *progressCopy, src, dest string) error {
	sfileStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	sfile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sfile.Close()

	var r io.Reader = sfile

	if progress != nil {
		progress.SetReader(sfile)
		progress.SetSize(sfileStat.Size())
		r = progress
	}

	dfile, err := s.sftp.Create(dest)
	if err != nil {
		return err
	}
	defer dfile.close()

	if _, err := dfile.readFrom(r, sfileStat.Size(), s.sftp.useConcurrency); err != nil {
		return err
	}

	return nil
}

func (s *sshSession) receivefile(progress *progressCopy, remoteSrc, dest string) error {
	remoteFile, err := s.sftp.Open(remoteSrc)
	if err != nil {
		return err
	}
	defer remoteFile.close()
	localFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	var w io.Writer = localFile
	stat, _ := s.sftp.Stat(remoteSrc)
	if progress != nil {
		progress.SetWriter(localFile)
		progress.SetSize(int64(stat.size))
		w = progress
	}
	defer localFile.Close()
	if _, err := remoteFile.writeTo(w, int64(stat.size), s.sftp.useConcurrency); err != nil {
		return err
	}
	return nil
}

func sftp(output io.Writer, src, dest string, sftpfunc sftpFunc, isReader bool, isConcurrency bool) error {
	var ch chan networkBytes
	var progress *progressCopy

	if output != nil {
		ch = make(chan networkBytes)
		defer close(ch)

		go func() {
			p := progressBar{}
			for n := range ch {
				output.Write([]byte(p.getProgressBarString(n, isConcurrency)))
			}
		}()
		progress = &progressCopy{
			reader:    nil,
			writer:    nil,
			bytesRead: 0,
			size:      0,
			ch:        ch,
			isReader:  isReader,
			isWriter:  !isReader,
		}
	}
	return sftpfunc(progress, src, dest)
}

func (s *sshSession) SendFile(progress io.Writer, src string, dest string) error {
	var err error
	src, err = filepath.Abs(src)
	if err != nil {
		return err
	}

	srcStat, err := os.Stat(src)

	if err != nil {
		return err
	}

	if srcStat.IsDir() {
		panic(fmt.Sprintf("SendFile: %s is directory, src is supposed to be file not directory", src))
	}

	dest, err = remoterealpath(s, dest)
	if err != nil {
		return err
	}

	isDir, _ := s.sftp.IsDir(dest)

	if isDir {
		dest = path.Join(dest, filepath.Base(src))
	}

	return sftp(progress, src, dest, s.sendfile, true, s.sftp.useConcurrency)
}

func (s *sshSession) SendDir(progress io.Writer, src string, dest string) error {
	var err error
	src, err = filepath.Abs(src)
	if err != nil {
		return err
	}

	dest, err = remoterealpath(s, dest)
	if err != nil {
		return err
	}

	dest = path.Join(dest, filepath.Base(src))

	if err = s.sftp.Mkdir(dest); err != nil {
		return err
	}
	files, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.Name() == "." || file.Name() == ".." {
			continue
		}
		if file.Type().IsDir() {
			s.SendDir(progress, path.Join(src, file.Name()), path.Join(dest, file.Name()))
		} else if file.Type().IsRegular() {
			start := time.Now()
			srcF := path.Join(src, file.Name())
			if err := s.sendfile(nil, srcF, path.Join(dest, file.Name())); err != nil {
				return err
			}
			if progress != nil {
				seconds := time.Since(start).Seconds()
				if seconds > 120 {
					progress.Write([]byte(fmt.Sprintf("%s - %0.2f min\n", srcF, seconds/60)))
				} else {
					progress.Write([]byte(fmt.Sprintf("%s - %0.2f sec\n", srcF, seconds)))
				}
			}
		}
	}
	return nil
}

func (s *sshSession) ReceiveRemoteFile(progress io.Writer, remoteSrc string, dest string) error {
	remoteSrc, err := remoterealpath(s, remoteSrc)
	if err != nil {
		return err
	}

	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}

	isRemoteRegularFile, _ := s.sftp.IsRegular(remoteSrc)
	if !isRemoteRegularFile {
		return fmt.Errorf("remote source is not a regular file")
	}
	stat, err := os.Stat(dest)
	if _, ok := err.(*os.PathError); !ok {
		if stat.IsDir() {
			dest = path.Join(dest, filepath.Base(remoteSrc))
		} else {
			return fmt.Errorf("file already exists")
		}
	}

	return sftp(progress, remoteSrc, dest, s.receivefile, false, s.sftp.useConcurrency)
}

func (s *sshSession) ReceiveRemoteDir(progress io.Writer, remoteDir string, destDir string) error {
	remoteDir, err := remoterealpath(s, remoteDir)
	if err != nil {
		return err
	}

	destDir, err = filepath.Abs(destDir)
	if err != nil {
		return err
	}

	destDir = path.Join(destDir, filepath.Base(remoteDir))

	remoteDirFiles, err := s.sftp.ReadDir(remoteDir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(destDir, 0755); err != nil {
		return err
	}

	for _, file := range remoteDirFiles {
		if file.stat.IsDir() {
			if err := s.ReceiveRemoteDir(progress, path.Join(remoteDir, file.name), destDir); err != nil {
				return err
			}
		} else if file.stat.IsRegular() {
			start := time.Now()
			rsrc := path.Join(remoteDir, file.name)
			if err := s.receivefile(nil, rsrc, path.Join(destDir, file.name)); err != nil {
				return err
			}
			if progress != nil {
				seconds := time.Since(start).Seconds()
				if seconds > 120 {
					progress.Write([]byte(fmt.Sprintf("%s - %0.2f min\n", rsrc, seconds/60)))
				} else {
					progress.Write([]byte(fmt.Sprintf("%s - %0.2f sec\n", rsrc, seconds)))
				}
			}
		}
	}
	return nil
}

func (s *sshSession) RemoveFile(srcpath string) error {
	srcpath, err := remoterealpath(s, srcpath)
	if err != nil {
		return err
	}
	return s.sftp.RemoveFile(srcpath)
}

func (s *sshSession) RemoveDirectory(srcdir string) error {
	srcdir, err := remoterealpath(s, srcdir)
	if err != nil {
		return err
	}
	return s.sftp.RemoveDirectory(srcdir)
}

func (s *sshSession) RemoveAll(srcdir string) error {
	srcdir, err := remoterealpath(s, srcdir)
	if err != nil {
		return err
	}
	files, err := s.sftp.ReadDir(srcdir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.stat.IsDir() {
			if err := s.RemoveAll(path.Join(srcdir, file.name)); err != nil {
				return err
			}
		} else {
			if err := s.sftp.RemoveFile(path.Join(srcdir, file.name)); err != nil {
				return err
			}
		}
	}
	return s.sftp.RemoveDirectory(srcdir)
}

func (s *sshSession) SetSftpConcurrency(concurrency bool) {
	s.sftp.useConcurrency = concurrency
}

func ConnectWithPassword(opt Options) (SshSession, error) {
	if knownHostKeyCallback == nil {
		return nil, fmt.Errorf("unable to read known hosts file")
	}

	session := &sshSession{}
	session.sftp = nil
	session.trustServerHost = opt.TrustServerHost
	config := &ssh.ClientConfig{
		User: opt.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(opt.Password),
		},
		HostKeyCallback: session.serverHostKey,
	}
	client, err := ssh.Dial("tcp", opt.Server+":"+strconv.Itoa(opt.Port), config)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to ssh server %s: %w", opt.Server, err)
	}
	session.client = client
	sftp, err := NewSFTPClient(client, opt.SftpConcurrency)
	if err != nil {
		return nil, err
	}
	session.sftp = sftp
	return session, nil
}

type Options struct {
	Server          string
	Port            int
	User            string
	Password        string
	TrustServerHost bool
	SftpConcurrency bool
}

func init() {
	if _, err := os.Stat(knownhostFile); os.IsNotExist(err) {
		os.Create(knownhostFile)
	}
	knownHostKeyCallback, _ = knownhosts.New(knownhostFile)
}
