package main

import (
	"os"

	copy "github.com/san-gg/mdeploy/cmd/copy"
	deploy "github.com/san-gg/mdeploy/cmd/deploy"
	exec "github.com/san-gg/mdeploy/cmd/exec"
	run "github.com/san-gg/mdeploy/cmd/run"

	"github.com/spf13/cobra"
)

var rootCmd cobra.Command = cobra.Command{
	Use:     "mdeploy",
	Short:   "mdeploy is a tool for remote server deployment and management over SSH",
	Version: "1.0",
}

func main() {
	rootCmd.AddCommand(
		deploy.DeployCommand(),
		exec.ExecCommand(),
		run.RunCommand(),
		copy.CopyCommand(),
	)
	rootCmd.PersistentFlags().Bool("plain", false, "print plain output")
	rootCmd.PersistentFlags().BoolP("trust", "T", false, "trust SSH server host key")
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
