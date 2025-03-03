package mdeploy

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

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

type Event struct {
	Id        uint32
	EventName string
	Status    Status
	Message   string
}
type ProgressBarReaderWriter interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	SetReader(reader io.Reader)
	SetWriter(writer io.Writer)
	SetSize(size int64)
}

type NetworkBytesChan struct {
	bytesRead int64
	size      int64
}

type remoteCopy struct {
	reader    io.Reader
	writer    io.Writer
	bytesRead int64
	size      int64
	startTime time.Time
	ch        chan<- NetworkBytesChan
}

func (c *remoteCopy) Read(p []byte) (n int, err error) {
	n, err = c.reader.Read(p)
	c.bytesRead += int64(n)
	c.ch <- NetworkBytesChan{
		c.bytesRead,
		c.size,
	}
	return
}

func (c *remoteCopy) Write(p []byte) (n int, err error) {
	n, err = c.writer.Write(p)
	c.bytesRead += int64(n)
	c.ch <- NetworkBytesChan{
		c.bytesRead,
		c.size,
	}
	return
}

func (c *remoteCopy) SetReader(reader io.Reader) {
	c.reader = reader
}

func (c *remoteCopy) SetWriter(writer io.Writer) {
	c.writer = writer
}

func (c *remoteCopy) SetSize(size int64) {
	c.size = size
}

type Progress interface {
	Start(wg *sync.WaitGroup)
	SetStatus(status *Event)
	StartTextEventOutput(ch <-chan string, e *Event)
	StartProgressBarEventOutput(ch chan NetworkBytesChan, e *Event) ProgressBarReaderWriter
	StartProgressBar(ch chan NetworkBytesChan) (ProgressBarReaderWriter, chan bool)
	WaitEventOutput(e *Event)
	Stop()
}

type ProgressOutput interface {
	GetOutput(window_width int) []string
	Wait()
}

type entryTextOutput struct {
	output    []string
	completed bool
	mtx       sync.Mutex
	done      chan bool
}

type entryProgressBarOutput struct {
	startTime  time.Time
	percentage float64
	speed      float64
	mtx        sync.Mutex
	completed  bool
	done       chan bool
}

func (eto *entryTextOutput) GetOutput(window_width int) []string {
	eto.mtx.Lock()
	defer eto.mtx.Unlock()
	len := len(eto.output)
	if eto.completed && len == 0 {
		eto.done <- true
		return nil
	}
	if !eto.completed && len <= 5 {
		return eto.output
	}
	eto.output = eto.output[1:]
	return eto.output
}

func (eto *entryTextOutput) Wait() {
	<-eto.done
	close(eto.done)
}

func getProgressBarString(percentage float64, speed float64, window_width int) string {
	var currspeed string
	if speed < 1024 {
		currspeed = fmt.Sprintf("%.2f KB/s", speed)
	} else {
		currspeed = fmt.Sprintf("%.2f MB/s", speed/(1024*1024))
	}
	if percentage == 100 {
		currspeed = ""
	}
	width := window_width/2 - 20
	p := strings.Repeat("=", int(percentage/100*float64(width)))
	pad := strings.Repeat(" ", width-len(p))
	return fmt.Sprintf("[%s>%s]  %.1f%%   %s", p, pad, percentage, currspeed)
}

func (eto *entryProgressBarOutput) GetOutput(window_width int) []string {
	eto.mtx.Lock()
	defer eto.mtx.Unlock()
	percentage := int(eto.percentage)
	if eto.completed && percentage == 100 {
		eto.done <- true
		return nil
	}
	return []string{getProgressBarString(eto.percentage, eto.speed, window_width)}
}

func (eto *entryProgressBarOutput) Wait() {
	<-eto.done
	close(eto.done)
}

func NewProgress(cmd *cobra.Command) Progress {
	output, err := cmd.Flags().GetBool("plain")
	if err != nil {
		panic(err)
	}
	if output {
		return newTTyPlainWritter()
	} else {
		return newTTyWritter()
	}
}
