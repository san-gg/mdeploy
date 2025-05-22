package progress

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/morikuni/aec"
	"github.com/san-gg/mdeploy/pkg/term"
)

type colorFunc func(string) string

var (
	statusColor        colorFunc = aec.YellowF.Apply
	successColor       colorFunc = aec.GreenF.Apply
	failedColor        colorFunc = aec.RedF.Apply
	countColor         colorFunc = aec.CyanF.Apply
	spinnerDoneColor   colorFunc = aec.MagentaF.With(aec.Bold).Apply
	spinnerFailedColor colorFunc = aec.RedF.With(aec.Bold).Apply
	eventOutputColor   colorFunc = aec.FullColorF(125, 125, 125).Apply
	ellipsesColor      colorFunc = aec.YellowF.Apply
	TimerColor         colorFunc = aec.BlueF.Apply
)

var (
	startedStatus   = statusColor(" Started ")
	runningStatus   = statusColor(" Running ")
	completedStatus = successColor("Completed")
	failedStatus    = failedColor(" Failed  ")
	cancelledStatus = failedColor("Cancelled")
	doneSpinner     = spinnerDoneColor("✔")
	failedSpinner   = spinnerFailedColor("✖")
)

type loader struct {
	time         time.Time
	index        int
	chars        []string
	color        colorFunc
	millisecDiff int64
}

func (s *loader) get() string {
	d := time.Since(s.time)
	if d.Milliseconds() > s.millisecDiff {
		s.index = (s.index + 1) % len(s.chars)
		s.time = time.Now()
	}
	return s.color(s.chars[s.index])
}

var strip loader = loader{
	time:         time.Now(),
	index:        0,
	chars:        []string{".", "..", "...", "...."},
	color:        ellipsesColor,
	millisecDiff: 250,
}

var spinner loader = loader{
	time:         time.Now(),
	index:        0,
	chars:        []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	color:        countColor,
	millisecDiff: 80,
}

type entry struct {
	event        Event
	eventElapsed struct {
		startTime      time.Time
		endTimeElapsed float64
	}
	output EventOutputWriter
}

type ttyWriter struct {
	events         map[uint32]*entry
	eventIds       []uint32
	done           chan bool
	mtx            sync.Mutex
	lastNumLines   uint
	completedCount int
	maxNameLen     int
}

func (t *ttyWriter) StartEvent() {
	go func() {
		defer func() {
			t.done <- true
		}()
		ticker := time.NewTicker(70 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				t.print()
				return
			case <-ticker.C:
				t.print()
			}
		}
	}()
}

func (t *ttyWriter) StopEvent() {
	t.done <- true
	<-t.done
	close(t.done)
	clear(t.eventIds)
	for e := range t.events {
		delete(t.events, e)
	}
}

func (t *ttyWriter) SetStatus(e *Event) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if t.done == nil {
		panic("ttyWritter is not started")
	}
	t.maxNameLen = max(t.maxNameLen, len(e.EventName))
	if x, ok := t.events[e.Id]; ok {
		if e.Status == COMPLETED || e.Status == FAILED || e.Status == CANCELLED {
			t.completedCount++
		}
		x.event.EventName = e.EventName
		x.event.Status = e.Status
		x.event.Message = e.Message
	} else {
		newEntry := &entry{
			event: *e,
			eventElapsed: struct {
				startTime      time.Time
				endTimeElapsed float64
			}{
				startTime:      time.Now(),
				endTimeElapsed: 0,
			},
			output: nil,
		}
		t.eventIds = append(t.eventIds, e.Id)
		t.events[e.Id] = newEntry
	}
}

func (t *ttyWriter) SetEventOutput(e *Event, eventOutput EventOutputWriter) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if eventOutput == nil {
		panic("eventOutput is nil")
	}
	if t.done == nil {
		panic("ttyWritter is not started")
	}
	ee, ok := t.events[e.Id]
	if !ok {
		panic("event not found")
	}
	ee.output = eventOutput
}

func (t *ttyWriter) UnSetEventOutput(e *Event) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if t.done == nil {
		panic("ttyWritter is not started")
	}
	ee, ok := t.events[e.Id]
	if !ok {
		panic("event not found")
	}
	ee.output = nil
}

func (t *ttyWriter) print() {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	if len(t.events) == 0 {
		return
	}
	for i := uint(0); i < t.lastNumLines; i++ {
		fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
		fmt.Fprint(os.Stdout, aec.Up(1))
	}
	fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
	t.lastNumLines = 0
	fmt.Fprint(os.Stdout, aec.Hide)
	defer func() {
		fmt.Fprint(os.Stdout, aec.Show)
	}()
	window_width, window_height, err := term.GetWinSize()
	window_height -= 4
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error getting window size: ", err)
		fmt.Fprintln(os.Stderr, "Stopping progress")
		t.StopEvent()
		return
	}
	if window_width < 60 {
		t.line("...", nil, window_width, nil)
		return
	}
	t.line(fmt.Sprintf("[+] Running %d/%d", t.completedCount, len(t.events)), countColor, window_width, nil)
outer:
	for _, eId := range t.eventIds {
		e := t.events[eId]
		if e.event.Status == STARTED || e.event.Status == RUNNING {
			e.eventElapsed.endTimeElapsed = time.Since(e.eventElapsed.startTime).Seconds()
		}
		if t.lastNumLines >= uint(window_height) {
			t.line(strip.get(), nil, window_width, nil)
			break
		}

		t.line("", nil, window_width, e)

		var count, totalCount int
		if len(t.eventIds) == 1 {
			totalCount = window_height - 5
		} else {
			totalCount = 5
		}
		if e.output != nil {
			for _, o := range e.output.GetOutput() {
				if t.lastNumLines >= uint(window_height) {
					t.line(strip.get(), nil, window_width, nil)
					break outer
				}
				if count == totalCount {
					break
				}
				t.line("        "+o, eventOutputColor, window_width, nil)
				count++
			}
		}
	}
}

func (t *ttyWriter) line(line string, color colorFunc, window_width int, e *entry) {
	if e != nil {
		var status, elapsed string
		spinner := spinner.get()
		switch e.event.Status {
		case STARTED:
			status = startedStatus
		case RUNNING:
			status = runningStatus
		case COMPLETED:
			status = completedStatus
			spinner = doneSpinner
		case FAILED:
			status = failedStatus
			spinner = failedSpinner
		case CANCELLED:
			status = cancelledStatus
			spinner = failedSpinner
		}
		if e.eventElapsed.endTimeElapsed <= 60 {
			elapsed = TimerColor(fmt.Sprintf("%.1fs", e.eventElapsed.endTimeElapsed))
		} else {
			elapsed = TimerColor(fmt.Sprintf("%.1fm", e.eventElapsed.endTimeElapsed/60))
		}
		line = fmt.Sprintf("  %s  %s%s%s ", spinner, e.event.EventName, strings.Repeat(" ", t.maxNameLen-len(e.event.EventName)+1), status)
		if window_width < len(line) {
			line = fmt.Sprintf("%s  %s", spinner, e.event.EventName)
		} else {
			msgArea := window_width - len(line) - len(elapsed) - 1
			msgLen := len(e.event.Message)
			if msgArea > msgLen {
				line = line + e.event.Message
				msgArea -= msgLen
			} else if msgArea > 8 {
				m := e.event.Message[:msgArea-4] + "... "
				line = line + m
				msgArea -= len(m)
			}
			line = line + strings.Repeat(" ", msgArea) + elapsed
		}
	} else if int(window_width) < len(line) {
		line = line[:window_width-8] + "..."
	}
	if color != nil {
		line = color(line)
	}
	fmt.Fprintln(os.Stdout, line)
	t.lastNumLines++
}

func newEventTTyWriter() *ttyWriter {
	return &ttyWriter{
		done:           make(chan bool),
		events:         make(map[uint32]*entry),
		lastNumLines:   0,
		completedCount: 0,
		maxNameLen:     0,
	}
}

type progressBarWriter struct {
	sync.Mutex
	finalProgressBar string
	progressBar      string
	isEventOutput    bool
	outprint         bool
	ticker           time.Time
}

func (e *progressBarWriter) Completed() {
	if !e.isEventOutput {
		fmt.Fprint(os.Stdout, aec.Up(1))
		fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
		fmt.Fprintln(os.Stdout, doneSpinner+e.finalProgressBar)
	}
}

func (e *progressBarWriter) Write(b []byte) (n int, err error) {
	n = len(b)
	if e.isEventOutput {
		e.Lock()
		defer e.Unlock()
		e.progressBar = string(b)
	} else if time.Since(e.ticker) > 1*time.Second {
		if e.outprint {
			fmt.Fprint(os.Stdout, aec.Up(1))
			fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
		}
		e.progressBar = " " + eventOutputColor(string(b))
		fmt.Fprintln(os.Stdout, spinner.get()+e.progressBar)
		e.outprint = true
		e.ticker = time.Now()
	} else if e.outprint {
		fmt.Fprint(os.Stdout, aec.Up(1))
		fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
		fmt.Fprintln(os.Stdout, spinner.get()+e.progressBar)
	}
	e.finalProgressBar = " " + eventOutputColor(string(b))
	return
}

func (e *progressBarWriter) GetOutput() []string {
	e.Lock()
	defer e.Unlock()
	return []string{e.progressBar}
}

func (e *progressBarWriter) Wait() {
	e.Lock()
	defer e.Unlock()
	e.progressBar = ""
}

func newProgressBarTTyWritter() *progressBarWriter {
	return &progressBarWriter{isEventOutput: false, ticker: time.Now()}
}

func newEventOutputProgressBarTTyWritter() *progressBarWriter {
	return &progressBarWriter{progressBar: "", isEventOutput: true}
}

type eventOutputWriter struct {
	sync.Mutex
	output              []string
	doWaitForCompletion bool
	done                chan bool
}

func (e *eventOutputWriter) Write(b []byte) (n int, err error) {
	n = len(b)
	e.Lock()
	defer e.Unlock()
	e.output = append(e.output, string(b))
	return
}

func (e *eventOutputWriter) GetOutput() []string {
	e.Lock()
	defer e.Unlock()
	if len(e.output) == 0 && e.doWaitForCompletion && e.done != nil {
		e.done <- true
		close(e.done)
		e.done = nil
		e.doWaitForCompletion = false
		return nil
	} else if len(e.output) == 0 {
		return nil
	} else if len(e.output) < 5 && e.doWaitForCompletion == false {
		return e.output
	}
	e.output = e.output[1:]
	return e.output
}

func (e *eventOutputWriter) Wait() {
	e.Lock()
	e.doWaitForCompletion = true
	e.Unlock()
	<-e.done
}

func newEventOutputTTyWriter() *eventOutputWriter {
	return &eventOutputWriter{
		output:              make([]string, 0),
		doWaitForCompletion: false,
		done:                make(chan bool),
	}
}
