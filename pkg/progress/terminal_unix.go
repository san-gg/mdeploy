//go:build !windows

package mdeploy

import (
	"os"

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
