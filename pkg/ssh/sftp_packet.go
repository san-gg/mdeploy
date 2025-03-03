package mdeploy

import (
	"encoding"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	sshFxpInit     = 1
	sshFxpVersion  = 2
	sshFxpOpen     = 3
	sshFxpClose    = 4
	sshFxpRead     = 5
	sshFxpWrite    = 6
	sshFxpLstat    = 7
	sshFxpFstat    = 8
	sshFxpOpendir  = 11
	sshFxpReaddir  = 12
	sshFxpRemove   = 13
	sshFxpRmdir    = 15
	sshFxpMkdir    = 14
	sshFxpRealpath = 16
	sshFxpStat     = 17
	sshFxpStatus   = 101
	sshFxpHandle   = 102
	sshFxpData     = 103
	sshFxpName     = 104
	sshFxpAttrs    = 105
)

const (
	sshFxOk               = 0
	sshFxEOF              = 1
	sshFxNoSuchFile       = 2
	sshFxPermissionDenied = 3
	sshFxFailure          = 4
	sshFxBadMessage       = 5
	sshFxNoConnection     = 6
	sshFxConnectionLost   = 7
	sshFxOPUnsupported    = 8
)

var (
	maxMsgLength   uint32 = 256 * 1024
	errLongPacket         = errors.New("packet too long")
	errShortPacket        = errors.New("packet too short")
)

type fx uint8

func (f fx) String() string {
	switch f {
	case sshFxOk:
		return "SSH_FX_OK"
	case sshFxEOF:
		return "SSH_FX_EOF"
	case sshFxNoSuchFile:
		return "SSH_FX_NO_SUCH_FILE"
	case sshFxPermissionDenied:
		return "SSH_FX_PERMISSION_DENIED"
	case sshFxFailure:
		return "SSH_FX_FAILURE"
	case sshFxBadMessage:
		return "SSH_FX_BAD_MESSAGE"
	case sshFxNoConnection:
		return "SSH_FX_NO_CONNECTION"
	case sshFxConnectionLost:
		return "SSH_FX_CONNECTION_LOST"
	case sshFxOPUnsupported:
		return "SSH_FX_OP_UNSUPPORTED"
	default:
		return "unknown"
	}
}

type StatusError struct {
	Code      uint32
	msg, lang string
}

func (s *StatusError) Error() string {
	return fmt.Sprintf("sftp: %q (%v)", s.msg, fx(s.Code))
}

func marshalUint32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func marshalUint64(b []byte, v uint64) []byte {
	return marshalUint32(marshalUint32(b, uint32(v>>32)), uint32(v))
}

func marshalString(b []byte, v string) []byte {
	return append(marshalUint32(b, uint32(len(v))), v...)
}

func marshalIDStringPacket(packetType byte, id uint32, str string) ([]byte, error) {
	l := 4 + 1 + 4 + // uint32(length) + byte(type) + uint32(id)
		4 + len(str)

	b := make([]byte, 4, l)
	b = append(b, packetType)
	b = marshalUint32(b, id)
	b = marshalString(b, str)

	return b, nil
}

func unmarshalUint32(b []byte) (uint32, []byte) {
	v := uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
	return v, b[4:]
}

func unmarshalUint64(b []byte) (uint64, []byte) {
	h, b := unmarshalUint32(b)
	l, b := unmarshalUint32(b)
	return uint64(h)<<32 | uint64(l), b
}

func unmarshalUint64Safe(b []byte) (uint64, []byte, error) {
	var v uint64
	if len(b) < 8 {
		return 0, nil, errShortPacket
	}
	v, b = unmarshalUint64(b)
	return v, b, nil
}

func unmarshalUint32Safe(b []byte) (uint32, []byte, error) {
	var v uint32
	if len(b) < 4 {
		return 0, nil, errShortPacket
	}
	v, b = unmarshalUint32(b)
	return v, b, nil
}

func unmarshalString(b []byte) (string, []byte) {
	n, b := unmarshalUint32(b)
	return string(b[:n]), b[n:]
}

func unmarshalStringSafe(b []byte) (string, []byte, error) {
	n, b, err := unmarshalUint32Safe(b)
	if err != nil {
		return "", nil, err
	}
	if int64(n) > int64(len(b)) {
		return "", nil, errShortPacket
	}
	return string(b[:n]), b[n:], nil
}

func unmarshalIDString(b []byte, id *uint32, str *string) error {
	var err error
	*id, b, err = unmarshalUint32Safe(b)
	if err != nil {
		return err
	}
	*str, _, err = unmarshalStringSafe(b)
	return err
}

func unmarshalExtensionPair(b []byte) (extensionPair, []byte, error) {
	var ep extensionPair
	var err error
	ep.Name, b, err = unmarshalStringSafe(b)
	if err != nil {
		return ep, b, err
	}
	ep.Data, b, err = unmarshalStringSafe(b)
	return ep, b, err
}

func unmarshalAttrs(b []byte) (*fileStat, []byte, error) {
	flags, b, err := unmarshalUint32Safe(b)
	if err != nil {
		return nil, b, err
	}
	return unmarshalFileStat(flags, b)
}

func unmarshalFileStat(flags uint32, b []byte) (*fileStat, []byte, error) {
	var fs fileStat
	var err error

	if flags&sshFileXferAttrSize == sshFileXferAttrSize {
		fs.size, b, err = unmarshalUint64Safe(b)
		if err != nil {
			return nil, b, err
		}
	}
	if flags&sshFileXferAttrUIDGID == sshFileXferAttrUIDGID {
		fs.uid, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}
		fs.gid, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}
	}
	if flags&sshFileXferAttrPermissions == sshFileXferAttrPermissions {
		fs.mode, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}
	}
	if flags&sshFileXferAttrACmodTime == sshFileXferAttrACmodTime {
		fs.atime, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}
		fs.mtime, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}
	}
	if flags&sshFileXferAttrExtended == sshFileXferAttrExtended {
		var count uint32
		count, b, err = unmarshalUint32Safe(b)
		if err != nil {
			return nil, b, err
		}

		ext := make([]statExtended, count)
		for i := uint32(0); i < count; i++ {
			var typ string
			var data string
			typ, b, err = unmarshalStringSafe(b)
			if err != nil {
				return nil, b, err
			}
			data, b, err = unmarshalStringSafe(b)
			if err != nil {
				return nil, b, err
			}
			ext[i] = statExtended{
				extType: typ,
				extData: data,
			}
		}
		fs.extended = ext
	}
	return &fs, b, nil
}

func unmarshalStatus(id uint32, data []byte) error {
	sid, data := unmarshalUint32(data)
	if sid != id {
		return &unexpectedIDErr{id, sid}
	}
	code, data := unmarshalUint32(data)
	msg, data, _ := unmarshalStringSafe(data)
	lang, _, _ := unmarshalStringSafe(data)
	return &StatusError{
		Code: code,
		msg:  msg,
		lang: lang,
	}
}

type extensionPair struct {
	Name string
	Data string
}

type sshFxInitPacket struct {
	Version    uint32
	Extensions []extensionPair
}

func (p *sshFxInitPacket) MarshalBinary() ([]byte, error) {
	l := 4 + 1 + 4 // uint32(length) + byte(type) + uint32(version)
	for _, e := range p.Extensions {
		l += 4 + len(e.Name) + 4 + len(e.Data)
	}

	b := make([]byte, 4, l)
	b = append(b, sshFxpInit)
	b = marshalUint32(b, p.Version)

	for _, e := range p.Extensions {
		b = marshalString(b, e.Name)
		b = marshalString(b, e.Data)
	}

	return b, nil
}

func (p *sshFxInitPacket) UnmarshalBinary(b []byte) error {
	var err error
	if p.Version, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	}
	for len(b) > 0 {
		var ep extensionPair
		ep, b, err = unmarshalExtensionPair(b)
		if err != nil {
			return err
		}
		p.Extensions = append(p.Extensions, ep)
	}
	return nil
}

type sshFxpOpenPacket struct {
	ID     uint32
	Path   string
	Pflags uint32
	Flags  uint32
	Attrs  interface{}
}

func (p *sshFxpOpenPacket) marshalPacket() ([]byte, []byte, error) {
	l := 4 + 1 + 4 + // uint32(length) + byte(type) + uint32(id)
		4 + len(p.Path) +
		4 + 4

	b := make([]byte, 4, l)
	b = append(b, sshFxpOpen)
	b = marshalUint32(b, p.ID)
	b = marshalString(b, p.Path)
	b = marshalUint32(b, p.Pflags)
	b = marshalUint32(b, p.Flags)

	return b, nil, nil
}

func (p *sshFxpOpenPacket) MarshalBinary() ([]byte, error) {
	header, payload, err := p.marshalPacket()
	return append(header, payload...), err
}

func (p *sshFxpOpenPacket) UnmarshalBinary(b []byte) error {
	var err error
	if p.ID, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if p.Path, b, err = unmarshalStringSafe(b); err != nil {
		return err
	} else if p.Pflags, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if p.Flags, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	}
	p.Attrs = b
	return nil
}

type sshFxpReadPacket struct {
	ID     uint32
	Len    uint32
	Offset uint64
	Handle string
}

func (p *sshFxpReadPacket) MarshalBinary() ([]byte, error) {
	l := 4 + 1 + 4 + // uint32(length) + byte(type) + uint32(id)
		4 + len(p.Handle) +
		8 + 4 // uint64 + uint32

	b := make([]byte, 4, l)
	b = append(b, sshFxpRead)
	b = marshalUint32(b, p.ID)
	b = marshalString(b, p.Handle)
	b = marshalUint64(b, p.Offset)
	b = marshalUint32(b, p.Len)

	return b, nil
}

func (p *sshFxpReadPacket) UnmarshalBinary(b []byte) error {
	var err error
	if p.ID, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if p.Handle, b, err = unmarshalStringSafe(b); err != nil {
		return err
	} else if p.Offset, b, err = unmarshalUint64Safe(b); err != nil {
		return err
	} else if p.Len, _, err = unmarshalUint32Safe(b); err != nil {
		return err
	}
	return nil
}

type sshFxpWritePacket struct {
	ID     uint32
	Length uint32
	Offset uint64
	Handle string
	Data   []byte
}

func (p *sshFxpWritePacket) marshalPacket() ([]byte, []byte, error) {
	l := 4 + 1 + 4 + // uint32(length) + byte(type) + uint32(id)
		4 + len(p.Handle) +
		8 + // uint64
		4

	b := make([]byte, 4, l)
	b = append(b, sshFxpWrite)
	b = marshalUint32(b, p.ID)
	b = marshalString(b, p.Handle)
	b = marshalUint64(b, p.Offset)
	b = marshalUint32(b, p.Length)

	return b, p.Data, nil
}

func (p *sshFxpWritePacket) MarshalBinary() ([]byte, error) {
	header, payload, err := p.marshalPacket()
	return append(header, payload...), err
}

func (p *sshFxpWritePacket) UnmarshalBinary(b []byte) error {
	var err error
	if p.ID, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if p.Handle, b, err = unmarshalStringSafe(b); err != nil {
		return err
	} else if p.Offset, b, err = unmarshalUint64Safe(b); err != nil {
		return err
	} else if p.Length, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if uint32(len(b)) < p.Length {
		return errShortPacket
	}

	p.Data = b[:p.Length]
	return nil
}

type sshFxpMkdirPacket struct {
	ID    uint32
	Flags uint32 // ignored
	Path  string
}

func (p *sshFxpMkdirPacket) MarshalBinary() ([]byte, error) {
	l := 4 + 1 + 4 + // uint32(length) + byte(type) + uint32(id)
		4 + len(p.Path) +
		4 // uint32

	b := make([]byte, 4, l)
	b = append(b, sshFxpMkdir)
	b = marshalUint32(b, p.ID)
	b = marshalString(b, p.Path)
	b = marshalUint32(b, p.Flags)

	return b, nil
}

func (p *sshFxpMkdirPacket) UnmarshalBinary(b []byte) error {
	var err error
	if p.ID, b, err = unmarshalUint32Safe(b); err != nil {
		return err
	} else if p.Path, b, err = unmarshalStringSafe(b); err != nil {
		return err
	} else if p.Flags, _, err = unmarshalUint32Safe(b); err != nil {
		return err
	}
	return nil
}

type sshFxpOpendirPacket struct {
	ID   uint32
	Path string
}

func (p *sshFxpOpendirPacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpOpendir, p.ID, p.Path)
}

func (p *sshFxpOpendirPacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Path)
}

type sshFxpReaddirPacket struct {
	ID     uint32
	Handle string
}

func (p *sshFxpReaddirPacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpReaddir, p.ID, p.Handle)
}

func (p *sshFxpReaddirPacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Handle)
}

type sshFxpStatPacket struct {
	ID   uint32
	Path string
}

func (p *sshFxpStatPacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpStat, p.ID, p.Path)
}

func (p *sshFxpStatPacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Path)
}

type sshFxpClosePacket struct {
	ID     uint32
	Handle string
}

func (p *sshFxpClosePacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpClose, p.ID, p.Handle)
}

func (p *sshFxpClosePacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Handle)
}

type sshFxpRealpathPacket struct {
	ID   uint32
	Path string
}

func (p *sshFxpRealpathPacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpRealpath, p.ID, p.Path)
}

func (p *sshFxpRealpathPacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Path)
}

type sshFxpRemovePacket struct {
	ID       uint32
	Filename string
}

func (p *sshFxpRemovePacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpRemove, p.ID, p.Filename)
}

func (p *sshFxpRemovePacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Filename)
}

type sshFxpRmdirPacket struct {
	ID   uint32
	Path string
}

func (p *sshFxpRmdirPacket) MarshalBinary() ([]byte, error) {
	return marshalIDStringPacket(sshFxpRmdir, p.ID, p.Path)
}

func (p *sshFxpRmdirPacket) UnmarshalBinary(b []byte) error {
	return unmarshalIDString(b, &p.ID, &p.Path)
}

type packetMarshaler interface {
	marshalPacket() (header, payload []byte, err error)
}

func marshalPacket(m encoding.BinaryMarshaler) (header, payload []byte, err error) {
	if m, ok := m.(packetMarshaler); ok {
		return m.marshalPacket()
	}

	header, err = m.MarshalBinary()
	return
}

type sftpconn struct {
	reader io.Reader
	write  io.WriteCloser
	sync.Mutex
}

func (c *sftpconn) Close() {
	c.write.Close()
}

func (c *sftpconn) sendPacket(m encoding.BinaryMarshaler) error {
	c.Lock()
	defer c.Unlock()
	header, payload, err := marshalPacket(m)
	if err != nil {
		return fmt.Errorf("binary marshaller failed: %w", err)
	}
	length := len(header) + len(payload) - 4 // subtract the uint32(length) from the start

	binary.BigEndian.PutUint32(header[:4], uint32(length))

	if _, err := c.write.Write(header); err != nil {
		return fmt.Errorf("failed to send packet: %w", err)
	}

	if len(payload) > 0 {
		if _, err := c.write.Write(payload); err != nil {
			return fmt.Errorf("failed to send packet payload: %w", err)
		}
	}

	return nil
}

func (c *sftpconn) recvPacket() (uint8, []byte, error) {
	b := make([]byte, 4)
	if _, err := io.ReadFull(c.reader, b); err != nil {
		return 0, nil, err
	}
	length, _ := unmarshalUint32(b)
	if length > maxMsgLength {
		return 0, nil, errLongPacket
	}
	if length == 0 {
		return 0, nil, errShortPacket
	}
	b = make([]byte, length)
	if _, err := io.ReadFull(c.reader, b); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return b[0], b[1:length], nil
}
