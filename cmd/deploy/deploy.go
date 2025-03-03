package mdeploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	progress "github.com/san-gg/mdeploy/pkg/progress"
	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
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

type deployEvent struct {
	event        *progress.Event
	taskProgress progress.Progress
	yml          *ymlConfig
}

func (e *deployEvent) SetStatus(status progress.Status, message string) {
	e.event.Status = status
	e.event.Message = message
	e.taskProgress.SetStatus(e.event)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}

	if envfile, err := os.ReadFile(".env"); err == nil {
		env := strings.Split(string(envfile), "\n")
		for _, e := range env {
			e = strings.TrimSpace(e)
			kv := strings.Split(e, "=")
			if len(kv) != 2 {
				return fmt.Errorf("invalid .env file")
			}
			os.Setenv(kv[0], kv[1])
		}
	}

	trust, err := cmd.Flags().GetBool("trust")
	if err != nil {
		panic(err)
	}

	progress := progress.NewProgress(cmd)
	wg := sync.WaitGroup{}
	wg.Add(2)
	progress.Start(&wg)
	go deploy(&wg, args, trust, progress)
	wg.Wait()
	return nil
}

func deploy(wg *sync.WaitGroup, args []string, trust bool, taskProgress progress.Progress) {
	defer wg.Done()
	defer taskProgress.Stop()
	wg2 := sync.WaitGroup{}
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
		wg2.Add(1)
		d.yml = yml
		go start(&wg2, d, trust)
	}
	wg2.Wait()
}

func start(wg *sync.WaitGroup, file *deployEvent, trusServerHostKey bool) {
	defer wg.Done()
	// initialize ssh client
	sshClient, err := ssh.ConnectWithPassword(file.yml.Credential.source, 22, file.yml.Credential.username, file.yml.Credential.password, trusServerHostKey)
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
			if err := copyToServer(file, sshClient, s.param["source"].(string), s.param["destination"].(string)); err != nil {
				file.SetStatus(progress.FAILED, COPYTOSERVER_TASK+" "+err.Error())
				return
			}
		case COPYFROMSERVER_TASK:
			if err := copyFromServer(file, sshClient, s.param["source"].(string), s.param["destination"].(string)); err != nil {
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

func copyToServer(file *deployEvent, sshclient ssh.SshSession, src string, dst string) error {
	if strings.Contains(src, "*") {
		return fmt.Errorf("wildcard not supported for deploy command")
	}
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if stat.Mode().IsRegular() {
		ch := make(chan progress.NetworkBytesChan)
		remoteCopyReader := file.taskProgress.StartProgressBarEventOutput(ch, file.event)
		defer func() {
			close(ch)
			file.taskProgress.WaitEventOutput(file.event)
		}()
		return sshclient.SendFile(remoteCopyReader, src, dst)
	} else if stat.Mode().IsDir() {
		ch := make(chan string)
		file.taskProgress.StartTextEventOutput(ch, file.event)
		defer func() {
			close(ch)
			file.taskProgress.WaitEventOutput(file.event)
		}()
		return sshclient.SendDir(nil, src, dst)
	}
	return fmt.Errorf("invalid %s to copy", src)
}

func copyFromServer(file *deployEvent, sshclient ssh.SshSession, src string, dst string) error {
	stat, err := sshclient.Stat(src)
	if err != nil {
		return err
	}
	if stat.IsRegular() {
		ch := make(chan progress.NetworkBytesChan)
		remoteCopyWriter := file.taskProgress.StartProgressBarEventOutput(ch, file.event)
		defer func() {
			close(ch)
			file.taskProgress.WaitEventOutput(file.event)
		}()
		return sshclient.ReceiveRemoteFile(remoteCopyWriter, src, dst)
	} else if stat.IsDir() {
		ch := make(chan string)
		file.taskProgress.StartTextEventOutput(ch, file.event)
		defer func() {
			close(ch)
			file.taskProgress.WaitEventOutput(file.event)
		}()
		return sshclient.ReceiveRemoteDir(ch, src, dst)
	} else {
		return fmt.Errorf("invalid %s to copy", src)
	}
}

func execCommand(file *deployEvent, sshclient ssh.SshSession, cmd string, args []string) error {
	ch := make(chan string)
	defer func() {
		close(ch)
		file.taskProgress.WaitEventOutput(file.event)
	}()
	file.taskProgress.StartTextEventOutput(ch, file.event)
	return sshclient.Exec(ch, cmd, args...)
}

func runCommand(file *deployEvent, sshclient ssh.SshSession, workingdir string, runfile string, args []string) error {
	if err := copyToServer(file, sshclient, runfile, workingdir); err != nil {
		return err
	}
	return execCommand(file, sshclient, "sh "+path.Join(workingdir, filepath.Base(runfile)), args)
}
