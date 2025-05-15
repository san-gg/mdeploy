//go:build !simulate

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/san-gg/mdeploy/cmd/deploy"
	"github.com/san-gg/mdeploy/cmd/ssh"

	"github.com/spf13/cobra"
)

var rootCmd cobra.Command = cobra.Command{
	Use:     "mdeploy",
	Short:   "mdeploy is a tool for remote server deployment and management over SSH",
	Version: "1.1",
}

func importEnvironment() error {
	if envfile, err := os.ReadFile(".env"); err == nil {
		env := strings.SplitSeq(string(envfile), "\n")
		for e := range env {
			e = strings.TrimSpace(e)
			kv := strings.Split(e, "=")
			if len(kv) != 2 {
				return fmt.Errorf("invalid .env file : %s", e)
			}
			os.Setenv(kv[0], kv[1])
		}
	}
	return nil
}

func main() {
	if err := importEnvironment(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	rootCmd.AddCommand(
		deploy.DeployCommand(),
		ssh.CopyCommand(),
		ssh.ExecCommand(),
		ssh.RunCommand(),
	)
	rootCmd.PersistentFlags().Bool("plain", false, "print plain output")
	rootCmd.PersistentFlags().BoolP("trust", "T", false, "trust SSH server host key")
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
