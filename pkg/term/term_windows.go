//go:build windows

package term

import (
	"os"
	"syscall"

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

func readPassword() ([]byte, error) {
	fd := syscall.Stdin
	var st uint32
	if err := windows.GetConsoleMode(windows.Handle(fd), &st); err != nil {
		return nil, err
	}
	old := st

	st &^= (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT)
	st |= (windows.ENABLE_PROCESSED_OUTPUT)
	if err := windows.SetConsoleMode(windows.Handle(fd), st); err != nil {
		return nil, err
	}

	defer windows.SetConsoleMode(windows.Handle(fd), old)

	var h windows.Handle
	p := windows.CurrentProcess()
	if err := windows.DuplicateHandle(p, windows.Handle(fd), p, &h, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(h), "stdin")
	defer f.Close()
	return readPasswordLine(f)
}
