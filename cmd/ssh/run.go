package ssh

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/san-gg/mdeploy/pkg/term"
	"github.com/spf13/cobra"
)

type runOptions struct {
	host string
	user string
}

var runOpt runOptions

func RunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run SCRIPT [ARG...]",
		Short: "Execute scripts on remote servers with arguments",
		Long:  "Upload and execute script files on remote servers.",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runCmd,
	}
	flags := cmd.Flags()
	flags.StringVarP(&runOpt.host, "host", "H", "", "server host")
	flags.StringVarP(&runOpt.user, "user", "U", "", "username")
	return cmd
}

func runCmd(cmd *cobra.Command, args []string) error {
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
		Server:          runOpt.host,
		Port:            22,
		User:            runOpt.user,
		Password:        string(pwd),
		TrustServerHost: trust,
		SftpConcurrency: false,
	})
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
	runfile := path.Join(workingdir, filepath.Base(args[0]))
	if err := sshExec(sshsession, fmt.Sprintf("sh %s %s", runfile, strings.Join(args[1:], " "))); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return nil
}
