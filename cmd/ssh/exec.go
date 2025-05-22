package ssh

import (
	"fmt"
	"os"

	"github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/san-gg/mdeploy/pkg/term"
	"github.com/spf13/cobra"
)

type execOptions struct {
	host string
	user string
}

var execOpt execOptions

func ExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec COMMAND",
		Short: "Execute commands on remote servers",
		Long:  "Run commands remotely and display their output.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  execCmd,
	}
	flags := cmd.Flags()
	flags.StringVarP(&execOpt.host, "host", "H", "", "server host")
	flags.StringVarP(&execOpt.user, "user", "U", "", "username")
	return cmd
}

func execCmd(cmd *cobra.Command, args []string) error {
	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}
	pwd, err := term.ReadPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to read password:", err)
		return nil
	}
	sshsession, err := ssh.ConnectWithPassword(ssh.Options{
		Server:          execOpt.host,
		Port:            22,
		User:            execOpt.user,
		Password:        string(pwd),
		TrustServerHost: trust,
		SftpConcurrency: false,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}
	defer sshsession.Close()
	if err := sshExec(sshsession, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "failed to execute command:", err)
		return nil
	}
	return nil
}

func sshExec(sshsession ssh.SshSession, cmd string) (err error) {
	err = sshsession.Exec(NewLineWriter{}, cmd)
	return
}

type NewLineWriter struct{}

func (w NewLineWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	fmt.Println(string(p))
	return
}
