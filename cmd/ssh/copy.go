package ssh

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/san-gg/mdeploy/pkg/progress"
	"github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

type sftpFunc func(progress io.Writer, src string, dst string) error

func CopyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy SOURCE DESTINATION",
		Short: "Copy files between local and remote servers",
		Long:  "Copy files or directories to or from remote servers.",
		Args:  cobra.ExactArgs(2),
		RunE:  copyCmd,
	}
	cmd.Flags().BoolP("parallel", "P", false, "use parallel copy")
	return cmd
}

func copyCmd(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("source and target are required")
	}
	if _, err := cmd.Flags().GetBool("trust"); err != nil {
		panic(err)
	}
	if _, err := cmd.Flags().GetBool("parallel"); err != nil {
		panic(err)
	}

	sip, suser, spwd, spath, sok := parseRemotePath(args[0])
	dip, duser, dpwd, dpath, dok := parseRemotePath(args[1])

	if sok && dok {
		return fmt.Errorf("remote to remote copy is not supported")
	}

	if sok {
		remoteReceive(spath, suser, sip, spwd, args[1], cmd)
		return nil
	} else if dok {
		remoteCopy(args[0], duser, dip, dpwd, dpath, cmd)
		return nil
	}

	return fmt.Errorf("source ... target are not valid")
}

func parseRemotePath(remote string) (ip string, user string, password string, path string, ok bool) {
	// user:pwd@ip:/path
	parts := strings.Split(remote, ":")
	if len(parts) != 3 {
		return
	}
	user = parts[0]
	path = parts[2]
	pwdIp := strings.Split(parts[1], "@")
	if len(pwdIp) != 2 {
		return
	}
	password = pwdIp[0]
	ip = pwdIp[1]
	ok = true
	return
}

func remoteCopy(src, user, ip, pwd, dst string, cmd *cobra.Command) {
	trust, _ := cmd.Flags().GetBool("trust")
	concurrency, _ := cmd.Flags().GetBool("parallel")
	sshsession, err := ssh.ConnectWithPassword(ssh.Options{
		Server:          ip,
		Port:            22,
		User:            user,
		Password:        pwd,
		TrustServerHost: trust,
		SftpConcurrency: concurrency,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer sshsession.Close()

	stat, err := os.Stat(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if stat.Mode().IsDir() {
		remoteSftp(src, dst, sshsession.SendDir, os.Stdout)
	} else if stat.Mode().IsRegular() {
		prog := progress.NewProgressBar(cmd)
		if err := remoteSftp(src, dst, sshsession.SendFile, prog); err == nil {
			prog.Completed()
		}
	} else {
		fmt.Fprintln(os.Stderr, "source is not a file or directory")
	}
}

func remoteReceive(src, user, ip, pwd, dst string, cmd *cobra.Command) {
	trust, _ := cmd.Flags().GetBool("trust")
	concurrency, _ := cmd.Flags().GetBool("parallel")
	sshsession, err := ssh.ConnectWithPassword(ssh.Options{
		Server:          ip,
		Port:            22,
		User:            user,
		Password:        pwd,
		TrustServerHost: trust,
		SftpConcurrency: concurrency,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer sshsession.Close()

	stat, err := sshsession.Stat(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	if stat.IsDir() {
		remoteSftp(src, dst, sshsession.ReceiveRemoteDir, os.Stdout)
	} else if stat.IsRegular() {
		prog := progress.NewProgressBar(cmd)
		if err := remoteSftp(src, dst, sshsession.ReceiveRemoteFile, prog); err == nil {
			prog.Completed()
		}
	} else {
		fmt.Fprintln(os.Stderr, "source is not a file or directory")
	}
}

func remoteSftp(src, dst string, fun sftpFunc, output io.Writer) error {
	if fun == nil {
		panic("fileFunc is nil")
	}
	if err := fun(output, src, dst); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	return nil
}
