//go:build simulate

package ssh

import (
	"io"
	"time"
)

type copysimulate struct {
	val int
}

func (c copysimulate) Read(p []byte) (n int, err error) {
	c.val += 100
	n = c.val
	return
}

func CopyProgressBar(output io.Writer) {
	ch := make(chan networkBytes)
	defer close(ch)

	go func() {
		p := progressBar{}
		for n := range ch {
			output.Write([]byte(p.getProgressBarString(n, true)))
		}
	}()
	progress := &progressCopy{
		reader:    nil,
		writer:    nil,
		bytesRead: 0,
		size:      0,
		ch:        ch,
		isReader:  true,
		isWriter:  false,
	}
	progress.SetSize(4000)
	progress.SetReader(copysimulate{})
	for i := 0; i < 4000; i += 100 {
		progress.Read([]byte{})
		time.Sleep(1 * time.Second)
	}
	time.Sleep(2 * time.Second)
}
