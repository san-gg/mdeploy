package term

import (
	"errors"
	"io"
	"os"
)

const (
	keyCtrlC = 3
	keyCtrlD = 4
)

var CtrlKeyError = errors.New("Ctrl key error")

func GetWinSize() (int, int, error) {
	return getWinSize()
}

func ReadPassword() (s string, err error) {
	os.Stdout.WriteString("Password: ")
	b, err := readPassword()
	if b != nil {
		s = string(b)
	}
	return
}

func readPasswordLine(io io.Reader) ([]byte, error) {
	var buf [1]byte
	var ret []byte
outer:
	for {
		_, err := io.Read(buf[:])
		if err != nil {
			return nil, err
		}
		switch buf[0] {
		case '\b':
			if len(ret) > 0 {
				ret = ret[:len(ret)-1]
			}
		case '\n':
			fallthrough
		case '\r':
			break outer
		case keyCtrlC:
			fallthrough
		case keyCtrlD:
			return nil, CtrlKeyError
		default:
			ret = append(ret, buf[0])
		}
	}
	return ret, nil
}
