//go:build simulate

package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	progress "github.com/san-gg/mdeploy/pkg/progress"
	ssh "github.com/san-gg/mdeploy/pkg/ssh"
	"github.com/spf13/cobra"
)

func eventOutputWriter(lines int, sleep int, w io.Writer) {
	for i := range lines {
		fmt.Fprintf(w, "Line %d", i)
		time.Sleep(time.Duration(sleep) * time.Second)
	}
}

func progressBarWriter(lines int, sleep int, w io.Writer) {
	for i := range lines {
		fmt.Fprint(w, fmt.Sprintf("[%s>%s]", strings.Repeat("=", i), strings.Repeat(" ", lines-i-1)))
		time.Sleep(time.Duration(sleep) * time.Second)
	}
}

func task(wg *sync.WaitGroup, event progress.ProgressEvent, i int, e progress.Event, cmd *cobra.Command) {
	defer wg.Done()
	e.Message = "EventOutputTest"
	event.SetStatus(&e)
	eo := progress.NewEventOutputWriter(cmd)
	////////////////////////////////////////////////////////////////////////////
	event.SetEventOutput(&e, eo)
	eventOutputWriter(i+10, min(i+2, 4), eo)
	eo.Wait()
	event.UnSetEventOutput(&e)
	////////////////////////////////////////////////////////////////////////////
	eo = progress.NewEventProgressBarWriter(cmd)
	event.SetEventOutput(&e, eo)
	progressBarWriter(i+10, min(i+2, 4), eo)
	eo.Wait()
	event.UnSetEventOutput(&e)
	////////////////////////////////////////////////////////////////////////////
	e.Status = progress.COMPLETED
	event.SetStatus(&e)
}
func simdeploy(cmd *cobra.Command) error {
	wg := &sync.WaitGroup{}
	event := progress.NewEventProgress(cmd)
	event.StartEvent()
	for i := 0; i < 5; i++ {
		e := progress.Event{
			Id:        uint32(i),
			EventName: fmt.Sprintf("Test %d", i),
			Status:    progress.RUNNING,
		}
		wg.Add(1)
		go task(wg, event, i, e, cmd)
	}
	wg.Wait()
	event.StopEvent()
	return nil
}
func simcopy(cmd *cobra.Command) {
	prog := progress.NewProgressBar(cmd)
	ssh.CopyProgressBar(prog)
	prog.Completed()
}
func simulate(cmd *cobra.Command, args []string) error {
	if args[0] == "deploy" {
		return simdeploy(cmd)
	} else if args[0] == "copy" {
		simcopy(cmd)
		return nil
	}
	return fmt.Errorf("unknown command: %s", args[0])
}

func main() {
	var rootCmd cobra.Command = cobra.Command{
		Use:   "simulate",
		Short: "simulate tty progress output",
		Args:  cobra.ExactArgs(1),
		RunE:  simulate,
	}
	rootCmd.PersistentFlags().Bool("plain", false, "print plain output")
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
