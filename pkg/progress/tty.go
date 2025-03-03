package mdeploy

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/morikuni/aec"
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
	startedStatus   = statusColor("Started  ")
	runningStatus   = statusColor("Running  ")
	completedStatus = successColor("Completed")
	failedStatus    = failedColor("Failed   ")
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
	chars:        []string{ellipsesColor("."), ellipsesColor(".."), ellipsesColor("..."), ellipsesColor("....")},
	color:        ellipsesColor,
	millisecDiff: 250,
}

type entry struct {
	event        Event
	eventElapsed struct {
		startTime      time.Time
		endTimeElapsed float64
	}
	output ProgressOutput
}

type ttyWritter struct {
	spinner        loader
	events         map[uint32]*entry
	eventIds       []uint32
	done           chan bool
	mtx            sync.Mutex
	lastNumLines   uint
	completedCount int
	maxNameLen     int
}

func (t *ttyWritter) Start(wg *sync.WaitGroup) {
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(70 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				t.print()
				close(t.done)
				return
			case <-ticker.C:
				t.print()
			}
		}
	}()
}

func (t *ttyWritter) Stop() {
	t.done <- true
}

func (t *ttyWritter) SetStatus(e *Event) {
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

func (t *ttyWritter) print() {
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
	window_width, window_height, err := getWinSize()
	window_height -= 4
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error getting window size: ", err)
		fmt.Fprintln(os.Stderr, "Stopping progress")
		t.Stop()
		return
	}
	if window_width < 60 {
		line(t, "...", nil, window_width, nil)
		return
	}
	line(t, fmt.Sprintf("[+] Running %d/%d", t.completedCount, len(t.events)), countColor, window_width, nil)
outer:
	for _, eId := range t.eventIds {
		e := t.events[eId]
		if e.event.Status == STARTED || e.event.Status == RUNNING {
			e.eventElapsed.endTimeElapsed = time.Since(e.eventElapsed.startTime).Seconds()
		}
		if t.lastNumLines >= uint(window_height) {
			line(t, strip.get(), nil, window_width, nil)
			break
		}

		line(t, "", nil, window_width, e)

		var count, totalCount int
		if len(t.eventIds) == 1 {
			totalCount = window_height - 5
		} else {
			totalCount = 5
		}
		for _, o := range getEventOutput(e, window_width) {
			if t.lastNumLines >= uint(window_height) {
				line(t, strip.get(), nil, window_width, nil)
				break outer
			}
			if count == totalCount {
				break
			}
			line(t, "        "+o, eventOutputColor, window_width, nil)
			count++
		}
	}
}

func line(t *ttyWritter, line string, color colorFunc, window_width int, e *entry) {
	if e != nil {
		var status, elapsed string
		spinner := t.spinner.get()
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
			elapsed = fmt.Sprintf("%.1fs", e.eventElapsed.endTimeElapsed)
		} else {
			elapsed = fmt.Sprintf("%.1fm", e.eventElapsed.endTimeElapsed/60)
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
			line = line + strings.Repeat(" ", msgArea) + TimerColor(elapsed)
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

func getEventOutput(e *entry, window_width int) []string {
	if e.output == nil {
		return nil
	}
	return e.output.GetOutput(window_width)
}

func (t *ttyWritter) WaitEventOutput(e *Event) {
	t.mtx.Lock()
	entry, ok := t.events[e.Id]
	if !ok {
		panic("Event wasn't added to stop")
	}
	t.mtx.Unlock()
	if entry.output == nil {
		panic("EventOutput not initialized")
	}
	entry.output.Wait()
	entry.output = nil
}

func (t *ttyWritter) StartTextEventOutput(ch <-chan string, e *Event) {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	entry, ok := t.events[e.Id]
	if !ok {
		panic("Event wasn't added to start text output")
	}
	if entry.output != nil {
		panic("StartTextEventOutput already started")
	}
	eto := &entryTextOutput{
		done:      make(chan bool, 1),
		completed: false,
	}
	entry.output = eto
	go func() {
		for o := range ch {
			eto.mtx.Lock()
			eto.output = append(eto.output, o)
			eto.mtx.Unlock()
		}
		eto.mtx.Lock()
		eto.completed = true
		eto.mtx.Unlock()
	}()
}

func (t *ttyWritter) StartProgressBarEventOutput(ch chan NetworkBytesChan, e *Event) ProgressBarReaderWriter {
	t.mtx.Lock()
	defer t.mtx.Unlock()
	entry, ok := t.events[e.Id]
	if !ok {
		panic("Event wasn't added to start progress bar")
	}
	if entry.output != nil {
		panic("StartTextEventOutput already started")
	}
	eto := &entryProgressBarOutput{
		startTime: time.Now(),
		done:      make(chan bool, 1),
		completed: false,
	}
	entry.output = eto
	go func() {
		for cp := range ch {
			percentage := float64(cp.bytesRead) / float64(cp.size) * 100
			speed := float64(cp.bytesRead) / time.Since(eto.startTime).Seconds()

			eto.mtx.Lock()
			eto.percentage = percentage
			eto.speed = speed
			eto.mtx.Unlock()
		}
		eto.mtx.Lock()
		eto.completed = true
		eto.mtx.Unlock()
	}()
	return &remoteCopy{
		startTime: time.Now(),
		ch:        ch,
	}
}

func (t *ttyWritter) StartProgressBar(ch chan NetworkBytesChan) (ProgressBarReaderWriter, chan bool) {
	done := make(chan bool)
	go func() {
		startTime := time.Now()
		written := false
		for cp := range ch {
			percentage := float64(cp.bytesRead) / float64(cp.size) * 100
			speed := float64(cp.bytesRead) / time.Since(startTime).Seconds()
			w, _, err := getWinSize()
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error getting window size: ", err)
				fmt.Fprintln(os.Stderr, "Stopping progress")
				return
			}
			fmt.Fprint(os.Stdout, aec.Hide)
			if written {
				fmt.Fprint(os.Stdout, aec.Up(1))
				fmt.Fprint(os.Stdout, aec.EraseLine(aec.EraseModes.All))
			}
			if w < 50 {
				fmt.Println("...")
				written = true
				continue
			}
			fmt.Println("  " + getProgressBarString(percentage, speed, w))
			fmt.Fprint(os.Stdout, aec.Show)
			written = true
		}
		done <- true
	}()
	return &remoteCopy{
		ch:        ch,
		startTime: time.Now(),
	}, done
}

func newTTyWritter() *ttyWritter {
	return &ttyWritter{
		done:           make(chan bool),
		events:         make(map[uint32]*entry),
		lastNumLines:   0,
		completedCount: 0,
		maxNameLen:     0,
		spinner: loader{
			time:         time.Now(),
			index:        0,
			chars:        []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
			color:        countColor,
			millisecDiff: 80,
		},
	}
}
