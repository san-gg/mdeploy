package mdeploy

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

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	p "github.com/san-gg/mdeploy/pkg/progress"
)

type FileStat interface {
	IsDir() bool
	IsRegular() bool
}

type SshSession interface {
	Stat(path string) (FileStat, error)
	SendDir(output chan<- string, src, dst string) error
	SendFile(progress p.ProgressBarReaderWriter, src, dst string) error
	Exec(output chan<- string, cmd string, param ...string) error
	ReceiveRemoteFile(progress p.ProgressBarReaderWriter, remoteSrc, dst string) error
	ReceiveRemoteDir(output chan<- string, remoteDir, dst string) error
	RemoveFile(path string) error
	Mkdir(path string) error
	RemoveDirectory(path string) error
	RemoveAll(srcdir string) error
	Close()
}

const knownhostFile = ".known_hosts"

var knownHostKeyCallback ssh.HostKeyCallback

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

func (s *sshSession) Exec(output chan<- string, command string, args ...string) error {
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
	if output != nil {
		done := make(chan bool, 2)
		defer func() {
			<-done
			<-done
			close(done)
		}()
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				output <- scanner.Text()
			}
			done <- true
		}()
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				output <- scanner.Text()
			}
			done <- true
		}()
	}
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

func sendfile(progress p.ProgressBarReaderWriter, s *sshSession, src string, dest string) error {
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
	if _, err := dfile.readFrom(r); err != nil {
		return err
	}
	return nil
}

func (s *sshSession) SendFile(progress p.ProgressBarReaderWriter, src string, dest string) error {
	var err error

	src, err = filepath.Abs(src)
	if err != nil {
		return err
	}

	dest, err = remoterealpath(s, dest)
	if err != nil {
		return err
	}

	isDir, _ := s.sftp.IsDir(dest)

	if isDir {
		dest = path.Join(dest, filepath.Base(src))
	}

	matches, err := filepath.Glob(src)

	if err != nil {
		return err
	}

	if len(matches) > 1 && isDir {
		return fmt.Errorf("cannot copy multiple files to a single file")
	}

	for _, file := range matches {
		if err = sendfile(progress, s, file, dest); err != nil {
			return err
		}
	}

	return nil
}

func (s *sshSession) SendDir(output chan<- string, src string, dest string) error {
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
			s.SendDir(output, path.Join(src, file.Name()), path.Join(dest, file.Name()))
		} else if file.Type().IsRegular() {
			if output != nil {
				output <- path.Join(dest, file.Name())
			}
			if err := sendfile(nil, s, path.Join(src, file.Name()), path.Join(dest, file.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func receivefile(progress p.ProgressBarReaderWriter, s *sshSession, remoteSrc string, dest string) error {
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
	if progress != nil {
		stat, _ := s.sftp.Stat(remoteSrc)
		progress.SetWriter(localFile)
		progress.SetSize(int64(stat.size))
		w = progress
	}
	defer localFile.Close()
	if _, err := remoteFile.writeTo(w); err != nil {
		return err
	}
	return nil
}

func (s *sshSession) ReceiveRemoteFile(progress p.ProgressBarReaderWriter, remoteSrc string, dest string) error {
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
	return receivefile(progress, s, remoteSrc, dest)
}

func (s *sshSession) ReceiveRemoteDir(output chan<- string, remoteDir string, destDir string) error {
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
			if err := s.ReceiveRemoteDir(output, path.Join(remoteDir, file.name), destDir); err != nil {
				return err
			}
		} else if file.stat.IsRegular() {
			if output != nil {
				output <- path.Join(destDir, file.name)
			}
			if err := receivefile(nil, s, path.Join(remoteDir, file.name), path.Join(destDir, file.name)); err != nil {
				return err
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

func ConnectWithPassword(src string, port int, user string, password string, trustServerHost bool) (SshSession, error) {
	if knownHostKeyCallback == nil {
		return nil, fmt.Errorf("known hosts file not found")
	}

	session := &sshSession{}
	session.sftp = nil
	session.trustServerHost = trustServerHost
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: session.serverHostKey,
	}
	client, err := ssh.Dial("tcp", src+":"+strconv.Itoa(port), config)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to ssh server %s: %w", src, err)
	}
	session.client = client
	sftp, err := NewSFTPClient(client)
	if err != nil {
		return nil, err
	}
	session.sftp = sftp
	return session, nil
}

func init() {
	if _, err := os.Stat(knownhostFile); os.IsNotExist(err) {
		os.Create(knownhostFile)
	}
	knownHostKeyCallback, _ = knownhosts.New(knownhostFile)
}
