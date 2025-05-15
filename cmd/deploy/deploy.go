package deploy

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/san-gg/mdeploy/pkg/progress"
	"github.com/san-gg/mdeploy/pkg/ssh"
)

func DeployCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy YAML_FILE...",
		Short: "Deploy using configuration from YAML files",
		Long: `Deploy connects to remote servers via SSH and executes tasks defined in the YAML configuration files.
Tasks can include copying files, executing commands, running scripts, and more.
Each YAML file defines connection credentials and a sequence of deployment steps.`,
		Args: cobra.MinimumNArgs(1),
		RunE: runDeploy,
	}
	return cmd
}

var deployCommand struct {
	cmd  *cobra.Command
	args []string
}

type deployEvent struct {
	event        *progress.Event
	taskProgress progress.ProgressEvent
	yml          *ymlConfig
}

func (e *deployEvent) SetStatus(status progress.Status, message string) {
	e.event.Status = status
	e.event.Message = message
	e.taskProgress.SetStatus(e.event)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}
	deployCommand = struct {
		cmd  *cobra.Command
		args []string
	}{
		cmd:  cmd,
		args: args,
	}
	progress := progress.NewEventProgress(cmd)
	progress.StartEvent()
	deploy(args, trust, progress)
	progress.StopEvent()
	return nil
}

func deploy(args []string, trust bool, taskProgress progress.ProgressEvent) {
	wg := sync.WaitGroup{}
	server := make(map[string]any)
	id := uint32(0)
	for _, file := range args {
		id += 1
		d := &deployEvent{
			event:        &progress.Event{Id: id, EventName: filepath.Base(file)},
			taskProgress: taskProgress,
		}
		yml, err := parseYml(file)
		if err != nil {
			d.SetStatus(progress.FAILED, "invalid yaml file : "+file)
			continue
		}
		if err := validateYml(yml); err != nil {
			d.SetStatus(progress.FAILED, err.Error())
			continue
		}
		if _, ok := server[yml.Credential.source+"@"+yml.Credential.username]; ok {
			d.SetStatus(progress.CANCELLED, "duplicate server entry : "+yml.Credential.source+"@"+yml.Credential.username)
			continue
		}
		server[yml.Credential.source+"@"+yml.Credential.username] = nil
		if yml.Name != "" {
			d.event.EventName = yml.Name
		}
		d.SetStatus(progress.STARTED, "Starting...")
		wg.Add(1)
		d.yml = yml
		go start(&wg, d, trust)
	}
	wg.Wait()
}

func start(wg *sync.WaitGroup, file *deployEvent, trusServerHostKey bool) {
	defer wg.Done()
	// initialize ssh client
	sshClient, err := ssh.ConnectWithPassword(ssh.Options{
		Server:          file.yml.Credential.source,
		Port:            22,
		User:            file.yml.Credential.username,
		Password:        file.yml.Credential.password,
		TrustServerHost: trusServerHostKey,
		SftpConcurrency: false,
	})
	if err != nil {
		file.SetStatus(progress.FAILED, err.Error())
		return
	}
	defer sshClient.Close()
	// create workingspace directory
	workingDirectory := ".mdeploy"
	sshClient.RemoveAll(workingDirectory)
	if err := sshClient.Mkdir(workingDirectory); err != nil {
		file.SetStatus(progress.FAILED, "Failed to create working directory")
	}
	// run tasks
	for _, s := range file.yml.Steps {
		file.SetStatus(progress.RUNNING, s.task+" "+s.description)
		switch s.task {
		case COPYTOSERVER_TASK:
			if err := copyToRemote(file, sshClient, s.param); err != nil {
				file.SetStatus(progress.FAILED, COPYTOSERVER_TASK+" "+err.Error())
				return
			}
		case COPYFROMSERVER_TASK:
			if err := copyFromRemote(file, sshClient, s.param); err != nil {
				file.SetStatus(progress.FAILED, COPYFROMSERVER_TASK+" "+err.Error())
				return
			}
		case RUN_TASK:
			param := strings.Split(s.param["file"].(string), " ")
			if err := runCommand(file, sshClient, workingDirectory, param[0], param[1:]); err != nil {
				file.SetStatus(progress.FAILED, RUN_TASK+" "+err.Error())
				return
			}
		case EXEC_TASK:
			if err := execCommand(file, sshClient, s.param["command"].(string), nil); err != nil {
				file.SetStatus(progress.FAILED, EXEC_TASK+" "+err.Error())
				return
			}
		case DELAY_TASK:
			sec := s.param["seconds"].(int)
			time.Sleep(time.Duration(sec) * time.Second)
		default:
			file.SetStatus(progress.FAILED, "Invalid Task : "+s.task)
			return
		}
	}
	file.SetStatus(progress.COMPLETED, "Completed")
}

type sftpFunc func(o io.Writer, src, dst string) error

func remoteSftpFile(src, dst string, fun sftpFunc, file *deployEvent) error {
	progressBar := progress.NewEventProgressBarWriter(deployCommand.cmd)
	file.taskProgress.SetEventOutput(file.event, progressBar)
	defer func() {
		progressBar.Wait()
		file.taskProgress.UnSetEventOutput(file.event)
	}()
	return fun(progressBar, src, dst)
}

func remoteSftpDir(src, dst string, fun sftpFunc, file *deployEvent) error {
	eventOutput := progress.NewEventOutputWriter(deployCommand.cmd)
	defer func() {
		eventOutput.Wait()
		file.taskProgress.UnSetEventOutput(file.event)
	}()
	return fun(eventOutput, src, dst)
}

func copyToRemote(file *deployEvent, sshclient ssh.SshSession, option map[string]any) error {
	src := option["source"].(string)
	dst := option["destination"].(string)
	sshclient.SetSftpConcurrency(false)
	if c, ok := option["parallel"].(bool); ok {
		sshclient.SetSftpConcurrency(c)
	}
	if strings.Contains(src, "*") {
		return fmt.Errorf("%s: wildcard not supported for deploy command", COPYTOSERVER_TASK)
	}
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if stat.Mode().IsRegular() {
		return remoteSftpFile(src, dst, sshclient.SendFile, file)
	} else if stat.Mode().IsDir() {
		return remoteSftpDir(src, dst, sshclient.SendDir, file)
	}
	return fmt.Errorf("invalid %s to copy", src)
}

func copyFromRemote(file *deployEvent, sshclient ssh.SshSession, option map[string]any) error {
	src := option["source"].(string)
	dst := option["destination"].(string)
	sshclient.SetSftpConcurrency(false)
	if c, ok := option["parallel"].(bool); ok {
		sshclient.SetSftpConcurrency(c)
	}
	stat, err := sshclient.Stat(src)
	if err != nil {
		return err
	}
	if stat.IsRegular() {
		return remoteSftpFile(src, dst, sshclient.ReceiveRemoteFile, file)
	} else if stat.IsDir() {
		return remoteSftpDir(src, dst, sshclient.ReceiveRemoteDir, file)
	} else {
		return fmt.Errorf("invalid %s to copy", src)
	}
}

func execCommand(file *deployEvent, sshclient ssh.SshSession, cmd string, args []string) error {
	eventOutput := progress.NewEventOutputWriter(deployCommand.cmd)
	file.taskProgress.SetEventOutput(file.event, eventOutput)
	defer func() {
		eventOutput.Wait()
		file.taskProgress.UnSetEventOutput(file.event)
	}()
	return sshclient.Exec(eventOutput, cmd, args...)
}

func runCommand(file *deployEvent, sshclient ssh.SshSession, workingdir string, runfile string, args []string) error {
	opt := make(map[string]any)
	opt["source"] = runfile
	opt["destination"] = workingdir
	if err := copyToRemote(file, sshclient, opt); err != nil {
		return err
	}
	delete(opt, "source")
	delete(opt, "destination")
	return execCommand(file, sshclient, "sh "+path.Join(workingdir, filepath.Base(runfile)), args)
}
