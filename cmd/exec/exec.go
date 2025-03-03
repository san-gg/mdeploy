package mdeloy

import (
	"fmt"
	"os"

	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

type execOptions struct {
	source string
	user   string
	pwd    string
}

var options execOptions

func ExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec COMMAND [ARG...]",
		Short: "Execute commands on remote servers",
		Long:  "Run commands remotely and display their output.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runExec,
	}
	flags := cmd.Flags()
	flags.StringVarP(&options.source, "source", "s", "", "server host")
	flags.StringVarP(&options.user, "user", "u", "", "username")
	flags.StringVarP(&options.pwd, "password", "p", "", "password")
	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}
	sshsession, err := ssh.ConnectWithPassword(options.source, 22, options.user, options.pwd, trust)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}
	defer sshsession.Close()
	ch := make(chan string)
	defer close(ch)
	go func() {
		for out := range ch {
			fmt.Println(out)
		}
	}()
	if err := sshsession.Exec(ch, args[0]); err != nil {
		fmt.Println(err)
		return nil
	}
	return nil
}
