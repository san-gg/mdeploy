package mdeloy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

type runOptions struct {
	source string
	user   string
	pwd    string
}

var options runOptions

func RunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run SCRIPT [ARG...]",
		Short: "Execute scripts on remote servers with arguments",
		Long:  "Upload and execute script files on remote servers.",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runRun,
	}
	flags := cmd.Flags()
	flags.StringVarP(&options.source, "source", "s", "", "server host")
	flags.StringVarP(&options.user, "user", "u", "", "username")
	flags.StringVarP(&options.pwd, "password", "p", "", "password")
	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
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
	workingdir := ".mdeploy"
	sshsession.RemoveAll(workingdir)
	if err := sshsession.Mkdir(workingdir); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to create working directory")
		return nil
	}
	if err := sshsession.SendFile(nil, args[0], workingdir); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to send file")
		return nil
	}
	ch := make(chan string)
	defer close(ch)
	go func() {
		for out := range ch {
			fmt.Println(out)
		}
	}()
	runfile := path.Join(workingdir, filepath.Base(args[0]))
	if err := sshsession.Exec(ch, "sh "+runfile, args[1:]...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}
	return nil
}
