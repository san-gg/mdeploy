package mdeploy

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

const sftpProtocolVersion = 3 // https://filezilla-project.org/specs/draft-ietf-secsh-filexfer-02.txt

const (
	sshFxfRead   uint32 = 0x00000001
	sshFxfWrite  uint32 = 0x00000002
	sshFxfAppend uint32 = 0x00000004
	sshFxfCreat  uint32 = 0x00000008
	sshFxfTrunc  uint32 = 0x00000010
	sshFxfExcl   uint32 = 0x00000020
)

type fxp uint8
type unexpectedPacketErr struct{ want, got uint8 }
type unexpectedVersionErr struct{ want, got uint32 }
type unexpectedIDErr struct{ want, got uint32 }

func (f fxp) String() string {
	switch f {
	case sshFxpInit:
		return "SSH_FXP_INIT"
	case sshFxpVersion:
		return "SSH_FXP_VERSION"
	case sshFxpWrite:
		return "SSH_FXP_WRITE"
	case sshFxpMkdir:
		return "SSH_FXP_MKDIR"
	case sshFxpLstat:
		return "SSH_FXP_LSTAT"
	case sshFxpFstat:
		return "SSH_FXP_FSTAT"
	default:
		return "unknown"
	}
}

func (u *unexpectedPacketErr) Error() string {
	return fmt.Sprintf("sftp: unexpected packet: want %v, got %v", fxp(u.want), fxp(u.got))
}
func (u *unexpectedVersionErr) Error() string {
	return fmt.Sprintf("sftp: unexpected server version: want %v, got %v", u.want, u.got)
}
func (u *unexpectedIDErr) Error() string {
	return fmt.Sprintf("sftp: unexpected id: want %d, got %d", u.want, u.got)
}

func unimplementedPacketErr(u uint8) error {
	return fmt.Errorf("sftp: unimplemented packet type: got %v", fxp(u))
}

func unexpectedCount(want, got uint32) error {
	return fmt.Errorf("sftp: unexpected count: want %d, got %d", want, got)
}

func normaliseError(err error) error {
	switch err := err.(type) {
	case *StatusError:
		switch err.Code {
		case sshFxEOF:
			return io.EOF
		case sshFxNoSuchFile:
			return os.ErrNotExist
		case sshFxPermissionDenied:
			return os.ErrPermission
		case sshFxOk:
			return nil
		default:
			return err
		}
	default:
		return err
	}
}

type sftpclient struct {
	sftpconn
	nextid                uint32
	maxPacket             uint32
	maxConcurrentRequests int
}

func (s *sftpclient) Close() {
	s.sftpconn.Close()
}

func (s *sftpclient) nextID() uint32 {
	return atomic.AddUint32(&s.nextid, 1)
}

func (s *sftpclient) checkVersion() error {
	if err := s.sendPacket(&sshFxInitPacket{Version: sftpProtocolVersion}); err != nil {
		return fmt.Errorf("error sending init packet to server: %w", err)
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		if err == io.EOF {
			return fmt.Errorf("server unexpectedly closed connection: %w", io.ErrUnexpectedEOF)
		}
		return err
	}

	if typ != sshFxpVersion {
		return &unexpectedPacketErr{sshFxpVersion, typ}
	}

	version, _, err := unmarshalUint32Safe(data)
	if err != nil {
		return err
	}

	if version != sftpProtocolVersion {
		return &unexpectedVersionErr{sftpProtocolVersion, version}
	}

	return nil
}

func (s *sftpclient) RealPath(path string) (string, error) {
	id := s.nextID()
	if err := s.sftpconn.sendPacket(&sshFxpRealpathPacket{ID: id, Path: path}); err != nil {
		return "", err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return "", err
	}
	switch typ {
	case sshFxpName:
		sid, data := unmarshalUint32(data)
		if sid != id {
			return "", &unexpectedIDErr{id, sid}
		}
		count, data := unmarshalUint32(data)
		if count != 1 {
			return "", unexpectedCount(1, count)
		}
		filename, _ := unmarshalString(data) // ignore attributes
		return filename, nil
	case sshFxpStatus:
		return "", normaliseError(unmarshalStatus(id, data))
	default:
		return "", unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) Stat(path string) (*fileStat, error) {
	id := s.nextID()
	if err := s.sftpconn.sendPacket(&sshFxpStatPacket{ID: id, Path: path}); err != nil {
		return nil, err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return nil, err
	}
	switch typ {
	case sshFxpAttrs:
		sid, data := unmarshalUint32(data)
		if sid != id {
			return nil, &unexpectedIDErr{id, sid}
		}
		attr, _, err := unmarshalAttrs(data)
		if err != nil {
			return nil, err
		}
		return attr, nil
	case sshFxpStatus:
		return nil, normaliseError(unmarshalStatus(id, data))
	default:
		return nil, unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) IsRegular(path string) (bool, error) {
	attr, err := s.Stat(path)
	if err != nil {
		return false, err
	}
	return attr.IsRegular(), nil
}

func (s *sftpclient) IsDir(path string) (bool, error) {
	attr, err := s.Stat(path)
	if err != nil {
		return false, err
	}
	return attr.IsDir(), nil
}

func (s *sftpclient) open(path string, pflags uint32) (*file, error) {
	id := s.nextID()
	if err := s.sftpconn.sendPacket(&sshFxpOpenPacket{ID: id, Path: path, Pflags: pflags}); err != nil {
		return nil, err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return nil, err
	}
	switch typ {
	case sshFxpHandle:
		sid, data := unmarshalUint32(data)
		if sid != id {
			return nil, &unexpectedIDErr{id, sid}
		}
		handle, _ := unmarshalString(data)
		return &file{c: s, path: path, handle: handle}, nil
	case sshFxpStatus:
		return nil, normaliseError(unmarshalStatus(id, data))
	default:
		return nil, unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) Open(path string) (*file, error) {
	return s.open(path, sshFxfRead)
}

func (s *sftpclient) Create(path string) (*file, error) {
	return s.open(path, sshFxfRead|sshFxfWrite|sshFxfCreat|sshFxfTrunc)
}

func (s *sftpclient) Mkdir(path string) error {
	id := s.nextID()
	if err := s.sftpconn.sendPacket(&sshFxpMkdirPacket{ID: id, Path: path}); err != nil {
		return err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return err
	}
	switch typ {
	case sshFxpStatus:
		return normaliseError(unmarshalStatus(id, data))
	default:
		return unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) OpenDir(path string) (string, error) {
	id := s.nextID()
	if err := s.sftpconn.sendPacket((&sshFxpOpendirPacket{
		ID:   id,
		Path: path,
	})); err != nil {
		return "", err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return "", err
	}
	switch typ {
	case sshFxpHandle:
		sid, data := unmarshalUint32(data)
		if sid != id {
			return "", &unexpectedIDErr{id, sid}
		}
		handle, _ := unmarshalString(data)
		return handle, nil
	case sshFxpStatus:
		return "", normaliseError(unmarshalStatus(id, data))
	default:
		return "", unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) ReadDir(p string) ([]fileInfo, error) {
	handle, err := s.OpenDir(p)
	if err != nil {
		return nil, err
	}
	defer s.close(handle)
	var entries []fileInfo
	var done = false
	for !done {
		id := s.nextID()
		if err1 := s.sftpconn.sendPacket(&sshFxpReaddirPacket{
			ID:     id,
			Handle: handle,
		}); err1 != nil {
			err = err1
			break
		}
		typ, data, err1 := s.recvPacket()
		if err1 != nil {
			err = err1
			break
		}
		switch typ {
		case sshFxpName:
			sid, data := unmarshalUint32(data)
			if sid != id {
				return nil, &unexpectedIDErr{id, sid}
			}
			count, data := unmarshalUint32(data)
			for i := uint32(0); i < count; i++ {
				var filename string
				filename, data = unmarshalString(data)
				_, data = unmarshalString(data) // discard longname
				var attr *fileStat
				attr, data, err = unmarshalAttrs(data)
				if err != nil {
					return nil, err
				}
				if filename == "." || filename == ".." {
					continue
				}
				entries = append(entries, fileInfo{
					name: filename,
					stat: attr,
				})
			}
		case sshFxpStatus:
			err = normaliseError(unmarshalStatus(id, data))
			done = true
		default:
			return nil, unimplementedPacketErr(typ)
		}
	}
	if err == io.EOF {
		err = nil
	}
	return entries, err
}

func (s *sftpclient) close(handle string) error {
	id := s.nextID()
	if err := s.sendPacket(&sshFxpClosePacket{
		ID:     id,
		Handle: handle,
	}); err != nil {
		return err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return err
	}
	switch typ {
	case sshFxpStatus:
		return normaliseError(unmarshalStatus(id, data))
	default:
		return unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) RemoveFile(path string) error {
	id := s.nextID()
	if err := s.sendPacket(&sshFxpRemovePacket{
		ID:       id,
		Filename: path,
	}); err != nil {
		return err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return err
	}
	switch typ {
	case sshFxpStatus:
		return normaliseError(unmarshalStatus(id, data))
	default:
		return unimplementedPacketErr(typ)
	}
}

func (s *sftpclient) RemoveDirectory(path string) error {
	id := s.nextID()
	if err := s.sendPacket(&sshFxpRmdirPacket{
		ID:   id,
		Path: path,
	}); err != nil {
		return err
	}
	typ, data, err := s.recvPacket()
	if err != nil {
		return err
	}
	switch typ {
	case sshFxpStatus:
		return normaliseError(unmarshalStatus(id, data))
	default:
		return unimplementedPacketErr(typ)
	}
}

func NewSFTPClient(conn *ssh.Client) (*sftpclient, error) {
	s, err := conn.NewSession()
	if err != nil {
		return nil, err
	}
	if err := s.RequestSubsystem("sftp"); err != nil {
		return nil, err
	}
	pw, err := s.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, err := s.StdoutPipe()
	if err != nil {
		return nil, err
	}

	sftp := &sftpclient{
		sftpconn: sftpconn{
			reader: pr,
			write:  pw,
		},
		maxPacket:             192 * 1024,
		maxConcurrentRequests: 64,
	}

	if err := sftp.checkVersion(); err != nil {
		pw.Close()
		s.Close()
		return nil, fmt.Errorf("error sending init packet to server: %w", err)
	}

	return sftp, nil
}
