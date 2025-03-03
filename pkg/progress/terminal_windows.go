//go:build windows

package mdeploy

import (
	"os"

	"golang.org/x/sys/windows"
)

func getWinSize() (width int, height int, err error) {
	fd := os.Stdout.Fd()
	var info windows.ConsoleScreenBufferInfo
	if err = windows.GetConsoleScreenBufferInfo(windows.Handle(fd), &info); err != nil {
		return
	}

	width = int(info.Window.Right - info.Window.Left + 1)
	height = int(info.Window.Bottom - info.Window.Top + 1)
	return
}
