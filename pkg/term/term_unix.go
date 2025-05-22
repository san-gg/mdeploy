//go:build !windows

package term

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func getWinSize() (width int, height int, err error) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		err = os.NewSyscallError("GetWinsize", err)
	}
	width = int(ws.Col)
	height = int(ws.Row)
	return
}

type passwordReader int

func (pwd passwordReader) Read(p []byte) (n int, err error) {
	return unix.Read(int(pwd), p)
}

func readPassword() ([]byte, error) {
	fd := syscall.Stdin
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}

	newState := *termios
	newState.Lflag &^= (unix.ECHO | unix.ISIG | unix.ICANON)
	newState.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &newState); err != nil {
		return nil, err
	}

	defer func() {
		os.Stdout.WriteString("\n")
		unix.IoctlSetTermios(fd, unix.TCSETS, termios)
	}()
	return readPasswordLine(passwordReader(fd))
}
