package mdeploy

import (
	"fmt"
	"sync"
	"time"
)

type ttyPlainWritter struct{}

func (t *ttyPlainWritter) Start(wg *sync.WaitGroup) {}

func (t *ttyPlainWritter) Stop() {}

func (t *ttyPlainWritter) SetStatus(e *Event) {
	fmt.Println(e.EventName, ":", e.Message)
}
func (t *ttyPlainWritter) StartTextEventOutput(ch <-chan string, e *Event) {
	go func() {
		for o := range ch {
			fmt.Println(e.EventName, "::", o)
		}
	}()
}
func (t *ttyPlainWritter) StartProgressBarEventOutput(ch chan NetworkBytesChan, e *Event) ProgressBarReaderWriter {
	go func() {
		for cp := range ch {
			percentage := float64(cp.bytesRead) / float64(cp.size) * 100
			speed := float64(cp.bytesRead) / time.Since(time.Now()).Seconds()
			var currspeed string
			if speed < 1024 {
				currspeed = fmt.Sprintf("%.2f KB/s", speed)
			} else {
				currspeed = fmt.Sprintf("%.2f MB/s", speed/(1024*1024))
			}
			fmt.Printf("%s: %.2f%% %s\n", e.EventName, percentage, currspeed)
		}
	}()
	return &remoteCopy{
		startTime: time.Now(),
		ch:        ch,
	}
}
func (t *ttyPlainWritter) WaitEventOutput(e *Event) {
}

func (t *ttyPlainWritter) StartProgressBar(ch chan NetworkBytesChan) (ProgressBarReaderWriter, chan bool) {
	done := make(chan bool)
	go func() {
		startTime := time.Now()
		for cp := range ch {
			percentage := float64(cp.bytesRead) / float64(cp.size) * 100
			speed := float64(cp.bytesRead) / time.Since(startTime).Seconds()
			var currspeed string
			if speed < 1024 {
				currspeed = fmt.Sprintf("%.2f KB/s", speed)
			} else {
				currspeed = fmt.Sprintf("%.2f MB/s", speed/(1024*1024))
			}
			fmt.Printf("%.2f%% %s\n", percentage, currspeed)
		}
		done <- true
	}()
	return &remoteCopy{
		ch:        ch,
		startTime: time.Now(),
	}, done
}

func newTTyPlainWritter() *ttyPlainWritter {
	return &ttyPlainWritter{}
}
