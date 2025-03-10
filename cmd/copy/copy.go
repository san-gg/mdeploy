package mdeploy

import (
	"fmt"
	"os"
	"strings"

	progress "github.com/san-gg/mdeploy/pkg/progress"
	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

type fileProgressFunc func(progress progress.ProgressBarReaderWriter, src string, dst string) error
type dirProgressFunc func(output chan<- string, src string, dst string) error

func CopyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy SOURCE... DESTINATION",
		Short: "Copy files between local and remote servers",
		Long:  "Copy files or directories to or from remote servers.",
		Args:  cobra.ExactArgs(2),
		RunE:  runCopy,
	}
	return cmd
}

func runCopy(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("source and target are required")
	}
	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}

	sip, suser, spwd, spath, sok := parseRemotePath(args[0])
	dip, duser, dpwd, dpath, dok := parseRemotePath(args[1])

	if sok && dok {
		return fmt.Errorf("remote to remote copy is not supported")
	}

	if sok {
		remoteReceive(spath, suser, sip, spwd, args[1], trust, cmd)
		return nil
	} else if dok {
		remoteCopy(args[0], duser, dip, dpwd, dpath, trust, cmd)
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

func remoteCopy(src, user, ip, pwd, dst string, trust bool, cmd *cobra.Command) {
	sshsession, err := ssh.ConnectWithPassword(ip, 22, user, pwd, trust)
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
		dirProgress(src, dst, sshsession.SendDir)
	} else if stat.Mode().IsRegular() {
		fileProgress(src, dst, sshsession.SendFile, cmd)
	} else {
		fmt.Fprintln(os.Stderr, "source is not a file or directory")
	}
}

func remoteReceive(src, user, ip, pwd, dst string, trust bool, cmd *cobra.Command) {
	sshsession, err := ssh.ConnectWithPassword(ip, 22, user, pwd, trust)
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
		dirProgress(src, dst, sshsession.ReceiveRemoteDir)
	} else if stat.IsRegular() {
		fileProgress(src, dst, sshsession.ReceiveRemoteFile, cmd)
	} else {
		fmt.Fprintln(os.Stderr, "source is not a file or directory")
	}
}

func fileProgress(src string, dst string, fileFunc fileProgressFunc, cmd *cobra.Command) {
	if fileFunc == nil {
		panic("fileFunc is nil")
	}
	ch := make(chan progress.NetworkBytesChan)
	rw, done := progress.NewProgress(cmd).StartProgressBar(ch)
	defer func() {
		close(ch)
		<-done
	}()
	if err := fileFunc(rw, src, dst); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func dirProgress(src string, dst string, dirFunc dirProgressFunc) {
	if dirFunc == nil {
		panic("dirFunc is nil")
	}
	ch := make(chan string)
	defer close(ch)
	go func() {
		for out := range ch {
			fmt.Println(out)
		}
	}()
	if err := dirFunc(ch, src, dst); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
