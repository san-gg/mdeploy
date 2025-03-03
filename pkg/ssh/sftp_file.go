package mdeploy

import (
	"io"
	"os"
	"sync"
)

const (
	sshFileXferAttrSize        = 0x00000001
	sshFileXferAttrUIDGID      = 0x00000002
	sshFileXferAttrPermissions = 0x00000004
	sshFileXferAttrACmodTime   = 0x00000008
	sshFileXferAttrExtended    = 0x80000000
)

const (
	modeDir     uint32 = 0x4000 // S_IFDIR
	modeType    uint32 = 0xF000 // S_IFMT
	modeRegular uint32 = 0x8000 // S_IFREG
)

type fileInfo struct {
	name string
	stat *fileStat
}

type fileStat struct {
	size     uint64
	mode     uint32
	mtime    uint32
	atime    uint32
	uid      uint32
	gid      uint32
	extended []statExtended
}
type statExtended struct {
	extType string
	extData string
}

func (fs *fileStat) IsDir() bool {
	return fs.mode&modeDir == modeDir
}

func (fs *fileStat) IsRegular() bool {
	return fs.mode&modeType == modeRegular
}

type file struct {
	c    *sftpclient
	path string

	mu     sync.RWMutex
	handle string
	offset int64
}

func (f *file) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle == "" {
		return os.ErrClosed
	}

	handle := f.handle
	f.handle = ""

	id := f.c.nextID()
	if err := f.c.sendPacket(&sshFxpClosePacket{ID: id, Handle: handle}); err != nil {
		return err
	}

	typ, data, err := f.c.recvPacket()
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

func (f *file) writeChunkAt(b []byte, off int64) (int, error) {
	if err := f.c.sendPacket(&sshFxpWritePacket{
		ID:     f.c.nextID(),
		Handle: f.handle,
		Offset: uint64(off),
		Length: uint32(len(b)),
		Data:   b,
	}); err != nil {
		return 0, err
	}
	typ, data, err := f.c.recvPacket()
	if err != nil {
		return 0, err
	}
	switch typ {
	case sshFxpStatus:
		id, _ := unmarshalUint32(data)
		if err := normaliseError(unmarshalStatus(id, data)); err != nil {
			return 0, err
		}
	default:
		return 0, unimplementedPacketErr(typ)
	}
	return len(b), nil
}

func (f *file) readFrom(r io.Reader) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle == "" {
		return 0, os.ErrClosed
	}

	//TO-DO: Add support for concurrent ReadFrom

	b := make([]byte, f.c.maxPacket)
	var read int64
	for {
		n, err := r.Read(b)
		if n < 0 {
			panic("sftp.file: reader returned negative count from Read")
		}
		if n > 0 {
			read += int64(n)
			m, err2 := f.writeChunkAt(b[:n], f.offset)
			f.offset += int64(m)
			if err == nil {
				err = err2
			}
		}

		if err != nil {
			if err == io.EOF {
				return read, nil // return nil explicitly
			}
			return read, err
		}
	}
}

func (f *file) readChunkAt(b []byte, off int64) (n int, err error) {
	for err == nil && n < len(b) {
		id := f.c.nextID()
		if err = f.c.sendPacket(&sshFxpReadPacket{
			ID:     id,
			Handle: f.handle,
			Offset: uint64(off) + uint64(n),
			Len:    uint32(len(b) - n),
		}); err != nil {
			return n, err
		}

		typ, data, err := f.c.recvPacket()
		if err != nil {
			return n, err
		}

		switch typ {
		case sshFxpStatus:
			return n, normaliseError(unmarshalStatus(id, data))

		case sshFxpData:
			sid, data := unmarshalUint32(data)
			if sid != id {
				return n, &unexpectedIDErr{id, sid}
			}
			l, data := unmarshalUint32(data)
			n += copy(b[n:], data[:l])

		default:
			return n, unimplementedPacketErr(typ)
		}
	}
	return
}

func (f *file) writeToSequential(W io.Writer) (written int64, err error) {
	b := make([]byte, f.c.maxPacket)

	for {
		n, err := f.readChunkAt(b, f.offset)
		if n < 0 {
			panic("sftp.file: reader returned negative count from Read")
		}
		if n > 0 {
			f.offset += int64(n)

			m, err := W.Write(b[:n])
			written += int64(m)

			if err != nil {
				return written, err
			}
		}
		if err != nil {
			if err == io.EOF {
				return written, nil
			}
			return written, err
		}
	}
}

func (f *file) writeTo(w io.Writer) (written int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.handle == "" {
		return 0, os.ErrClosed
	}
	return f.writeToSequential(w)
}
