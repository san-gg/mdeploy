package ssh

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/san-gg/mdeploy/pkg/progress"
)

type networkBytes struct {
	duration         time.Duration
	bytesTransferred int64
	bytesRead        int64
	totalBytes       int64
}

type progressCopy struct {
	reader    io.Reader
	writer    io.Writer
	bytesRead int64
	size      int64
	ch        chan<- networkBytes
	isReader  bool
	isWriter  bool
	startTime time.Time
}

func (c *progressCopy) Read(p []byte) (n int, err error) {
	if c.isWriter {
		panic("Read called on writer")
	}
	n, err = c.reader.Read(p)
	c.bytesRead += int64(n)
	if c.ch != nil {
		c.ch <- networkBytes{
			duration:         time.Since(c.startTime),
			bytesTransferred: int64(n),
			bytesRead:        c.bytesRead,
			totalBytes:       c.size,
		}
	}
	c.startTime = time.Now()
	return
}

func (c *progressCopy) Write(p []byte) (n int, err error) {
	if c.isReader {
		panic("Write called on reader")
	}
	n, err = c.writer.Write(p)
	c.bytesRead += int64(n)
	if c.ch != nil {
		c.ch <- networkBytes{
			duration:         time.Since(c.startTime),
			bytesTransferred: int64(n),
			bytesRead:        c.bytesRead,
			totalBytes:       c.size,
		}
	}
	c.startTime = time.Now()
	return
}

func (c *progressCopy) SetReader(reader io.Reader) {
	c.reader = reader
}

func (c *progressCopy) SetWriter(writer io.Writer) {
	c.writer = writer
}

func (c *progressCopy) SetSize(size int64) {
	c.size = size
}

type progressBar struct {
	speed float64 // per sec
}

func (p *progressBar) getProgressBarString(net networkBytes) string {
	var currspeed, bytesRead string
	if net.duration != 0 {
		curr_speed := float64(net.bytesTransferred) / net.duration.Seconds()
		p.speed = p.speed*0.7 + curr_speed*0.3 //EMA
		if p.speed < 1000 {
			currspeed = fmt.Sprintf("%.1f B/s", p.speed)
		} else if p.speed < (1000 * 1000) {
			currspeed = fmt.Sprintf("%.1f KB/s", p.speed/1024)
		} else if p.speed < (1000 * 1000 * 1000) {
			currspeed = fmt.Sprintf("%.1f MB/s", p.speed/(1024*1024))
		} else {
			currspeed = fmt.Sprintf("%.1f GB/s", p.speed/(1024*1024*1024))
		}
	}
	width, _, err := progress.GetWinSize()
	if err != nil {
		return fmt.Sprintf("Error getting window size: %v", err)
	}
	width = width/2 - 10
	if net.bytesRead < 1024 {
		bytesRead = fmt.Sprintf("%dB", net.bytesRead)
	} else if net.bytesRead >= 1024 && net.bytesRead < (1024*1024) {
		bytesRead = fmt.Sprintf("%dKB", net.bytesRead/1024)
	} else {
		bytesRead = fmt.Sprintf("%dMB", net.bytesRead/(1024*1024))
	}
	percentage := (float32(net.bytesRead) / float32(net.totalBytes)) * 100
	if percentage == 100 {
		currspeed = "--:--"
	}
	r := strings.Repeat("=", int(percentage*(float32(width)/100)))
	pad := strings.Repeat(" ", width-len(r))
	return fmt.Sprintf("[%s>%s]  %d%% %s  %s", r, pad, int(percentage), bytesRead, currspeed)
}
