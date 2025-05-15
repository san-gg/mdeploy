package progress

import (
	"fmt"
	"sync"
	"time"
)

type ttyPlainWritter struct {
	eventOutputCompletion map[string]chan bool
}

func (t *ttyPlainWritter) StartEvent() {}

func (t *ttyPlainWritter) StopEvent() {}

func (t *ttyPlainWritter) SetStatus(e *Event) {
	fmt.Println(e.EventName, ":", e.Message)
}

func (t *ttyPlainWritter) SetEventOutput(e *Event, eventOutput EventOutputWriter) {
	if _, ok := t.eventOutputCompletion[e.EventName]; ok {
		panic("EventOut already added: " + e.EventName)
	}
	t.eventOutputCompletion[e.EventName] = make(chan bool)
	go func(eventName, message string, eventOutput EventOutputWriter) {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.eventOutputCompletion[eventName]:
				for _, v := range eventOutput.GetOutput() {
					fmt.Println(eventName, ":", message, ":", v)
				}
				return
			case <-ticker.C:
				for _, v := range eventOutput.GetOutput() {
					fmt.Println(eventName, ":", message, ":", v)
				}
			}
		}
	}(e.EventName, e.Message, eventOutput)
}

func (t *ttyPlainWritter) UnSetEventOutput(e *Event) {
	if _, ok := t.eventOutputCompletion[e.EventName]; ok {
		t.eventOutputCompletion[e.EventName] <- true
		close(t.eventOutputCompletion[e.EventName])
		delete(t.eventOutputCompletion, e.EventName)
	}
}

func newEventTTyPlainWriter() *ttyPlainWritter {
	return &ttyPlainWritter{
		eventOutputCompletion: make(map[string]chan bool),
	}
}

type ttyOutputPlainWriter struct {
	sync.Mutex
	output        []string
	isEventOutput bool
}

func (p *ttyOutputPlainWriter) Completed() {}

func (p *ttyOutputPlainWriter) Write(b []byte) (n int, err error) {
	n = len(b)
	if p.isEventOutput {
		p.Lock()
		defer p.Unlock()
		p.output = append(p.output, string(b))
	} else {
		fmt.Println(string(b))
	}
	return
}

func (p *ttyOutputPlainWriter) GetOutput() []string {
	p.Lock()
	defer p.Unlock()
	if len(p.output) == 0 {
		return nil
	}
	t := p.output
	p.output = []string{}
	return t
}

func (p *ttyOutputPlainWriter) Wait() {}

func newTTyPlainWritter() *ttyOutputPlainWriter {
	return &ttyOutputPlainWriter{isEventOutput: false}
}

func newTTyOutputPlainWritter() *ttyOutputPlainWriter {
	return &ttyOutputPlainWriter{isEventOutput: true}
}
