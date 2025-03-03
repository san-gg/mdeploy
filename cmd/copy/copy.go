package mdeploy

import (
	"fmt"
	"os"
	"strings"

	progress "github.com/san-gg/mdeploy/pkg/progress"
	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

type copyOptions struct {
	srcPassword string
	dstPassword string
}

var options copyOptions

func CopyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy SOURCE... DESTINATION",
		Short: "Copy files between local and remote servers",
		Long:  "Copy files or directories to or from remote servers.",
		Args:  cobra.ExactArgs(2),
		RunE:  runCopy,
	}
	flags := cmd.Flags()
	flags.StringVar(&options.srcPassword, "src-password", "", "source password")
	flags.StringVar(&options.dstPassword, "dst-password", "", "destination password")
	return cmd
}

func runCopy(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("source and target are required")
	}
	if options.srcPassword != "" && options.dstPassword != "" {
		return fmt.Errorf("both source and target password are not allowed")
	}
	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}
	src := strings.Split(args[0], ":")
	dst := strings.Split(args[1], ":")
	if len(src) == 2 && len(dst) == 2 {
		return fmt.Errorf("server to server copy is not supported")
	} else if len(src) > 2 || len(dst) > 2 || (len(src) < 2 && len(dst) < 2) {
		return fmt.Errorf("source ... target are not valid")
	}
	if len(dst) == 2 {
		if options.dstPassword == "" {
			return fmt.Errorf("destination password is required")
		}
		dstUserIp := strings.Split(dst[0], "@")
		if len(dstUserIp) != 2 {
			return fmt.Errorf("destination user@ip is not valid")
		}
		sshsession, err := ssh.ConnectWithPassword(dstUserIp[1], 22, dstUserIp[0], options.dstPassword, trust)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		defer sshsession.Close()
		stat, err := os.Stat(src[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}

		if stat.Mode().IsDir() {
			ch := make(chan string)
			defer close(ch)
			go func() {
				for out := range ch {
					fmt.Println(out)
				}
			}()
			if err := sshsession.SendDir(ch, src[0], dst[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return nil
			}
		} else if stat.Mode().IsRegular() {
			ch := make(chan progress.NetworkBytesChan)
			rw, done := progress.NewProgress(cmd).StartProgressBar(ch)
			defer func() {
				close(ch)
				<-done
			}()
			if err := sshsession.SendFile(rw, src[0], dst[1]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "source is not a file or directory")
		}
	} else {
		if options.srcPassword == "" {
			return fmt.Errorf("source password is required")
		}
		srcUserIp := strings.Split(src[0], "@")
		if len(srcUserIp) != 2 {
			return fmt.Errorf("source user@ip is not valid")
		}
		sshsession, err := ssh.ConnectWithPassword(srcUserIp[1], 22, srcUserIp[0], options.srcPassword, trust)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		defer sshsession.Close()
		stat, err := sshsession.Stat(src[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		if stat.IsDir() {
			ch := make(chan string)
			defer close(ch)
			go func() {
				for out := range ch {
					fmt.Println(out)
				}
			}()
			if err := sshsession.SendDir(ch, src[1], dst[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return nil
			}
		} else if stat.IsRegular() {
			ch := make(chan progress.NetworkBytesChan)
			rw, done := progress.NewProgress(cmd).StartProgressBar(ch)
			defer func() {
				close(ch)
				<-done
			}()
			if err := sshsession.SendFile(rw, src[1], dst[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "source is not a file or directory")
		}
	}
	return nil
}
