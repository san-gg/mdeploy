package progress

import (
	"github.com/spf13/cobra"
)

type Status int

const (
	STARTED Status = iota
	RUNNING
	COMPLETED
	FAILED
	CANCELLED
)

type ProgressEvent interface {
	StartEvent()
	SetStatus(e *Event)
	SetEventOutput(e *Event, eventOutput EventOutputWriter)
	UnSetEventOutput(e *Event)
	StopEvent()
}

type EventOutputWriter interface {
	GetOutput() []string
	Write(p []byte) (n int, err error)
	Wait()
}

type Event struct {
	Id        uint32
	EventName string
	Status    Status
	Message   string
}

func NewEventProgress(cmd *cobra.Command) ProgressEvent {
	output, err := cmd.Flags().GetBool("plain")
	if err != nil {
		panic(err)
	}
	if output {
		return newEventTTyPlainWriter()
	} else {
		return newEventTTyWriter()
	}
}

func NewEventProgressBarWriter(cmd *cobra.Command) EventOutputWriter {
	output, err := cmd.Flags().GetBool("plain")
	if err != nil {
		panic(err)
	}
	if output {
		return newTTyOutputPlainWritter()
	} else {
		return newEventOutputProgressBarTTyWritter()
	}
}

func NewEventOutputWriter(cmd *cobra.Command) EventOutputWriter {
	output, err := cmd.Flags().GetBool("plain")
	if err != nil {
		panic(err)
	}
	if output {
		return newTTyOutputPlainWritter()
	} else {
		return newEventOutputTTyWriter()
	}
}

type ProgressBarWriter interface {
	Write(p []byte) (n int, err error)
	Completed()
}

func NewProgressBar(cmd *cobra.Command) ProgressBarWriter {
	output, err := cmd.Flags().GetBool("plain")
	if err != nil {
		panic(err)
	}

	if output {
		return newTTyPlainWritter()
	} else {
		return newProgressBarTTyWritter()
	}
}
