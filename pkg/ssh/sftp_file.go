package ssh

import (
	"encoding"
	"fmt"
	"io"
	"math"
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
	offset uint64
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

func (f *file) writeChunkAt(b []byte, off uint64) (int, error) {
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

func (f *file) readFrom(r io.Reader, size int64, useConcurrency bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if useConcurrency {
		return f.readFromConcurrency(r, size)
	}
	if f.handle == "" {
		return 0, os.ErrClosed
	}

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
			f.offset += uint64(m)
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

func (f *file) readChunkAt(b []byte, off uint64) (n int, err error) {
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
			f.offset += uint64(n)

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

func (f *file) writeTo(w io.Writer, size int64, useConcurrency bool) (written int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle == "" {
		return 0, os.ErrClosed
	}
	if useConcurrency {
		return f.writeToConcurrency(w, size)
	}
	return f.writeToSequential(w)
}

/////////////////////////////////////////////////////////////////////
/////////////////////// Concurrency /////////////////////////////////

type result struct {
	typ  byte
	data []byte
	err  error
}

type sftpFilePacket struct {
	sync.Mutex
	c                 *sftpclient
	inflight          map[uint32]chan result
	recvPacketRoutine bool
}

func (f *sftpFilePacket) getRemoveChannel(id uint32) chan result {
	f.Lock()
	defer f.Unlock()
	if ch, ok := f.inflight[id]; ok {
		delete(f.inflight, id)
		return ch
	}
	return nil
}
func (f *sftpFilePacket) sendPacket(sid uint32, res chan result, packet encoding.BinaryMarshaler) {
	f.Lock()
	defer f.Unlock()
	if !f.recvPacketRoutine {
		res <- result{err: fmt.Errorf("recvPacketRoutine is not running")}
		return
	}
	if _, ok := f.inflight[sid]; !ok {
		f.inflight[sid] = res
	} else {
		panic("channel already exists")
	}
	if err := f.c.sendPacket(packet); err != nil {
		res <- result{err: err}
	}
}

func (f *sftpFilePacket) recvPacket() {
	var (
		err  error
		data []byte
		typ  uint8
		sid  uint32
	)
	f.recvPacketRoutine = true
	for {
		typ, data, err = f.c.recvPacket()
		if err != nil {
			break
		}
		sid, _, err = unmarshalUint32Safe(data)
		if err != nil {
			break
		}

		ch := f.getRemoveChannel(sid)
		if ch == nil {
			break
		}
		ch <- result{typ: typ, data: data}
	}
	f.Lock()
	defer f.Unlock()
	f.recvPacketRoutine = false
	for _, ch := range f.inflight {
		ch <- result{err: err}
	}
}

func (f *file) readFromConcurrency(r io.Reader, size int64) (int64, error) {

	if size <= int64(f.c.maxPacket) {
		return f.readFrom(r, size, false)
	}
	concurrency64 := size/int64(f.c.maxPacket) + 1
	concurrency := int(min(concurrency64, int64(f.c.maxConcurrentRequests)))

	type work struct {
		id  uint32
		res chan result
		off uint64
	}
	type rwErr struct {
		off uint64
		err error
	}
	filePacket := &sftpFilePacket{
		c:        f.c,
		inflight: make(map[uint32]chan result),
	}
	cancel := make(chan struct{})
	errCh := make(chan rwErr)
	worker := make(chan work, concurrency)

	b := make([]byte, f.c.maxPacket)
	var read uint64
	go filePacket.recvPacket()
	defer func() {
		// defer to close the recvPacket routine
		f.c.sendPacket(&sshFxpWritePacket{
			ID:     f.c.nextID(),
			Handle: f.handle,
			Offset: 0,
			Length: 0,
			Data:   []byte{},
		})
	}()
	go func() {
		defer close(worker)
		var off uint64
		for {
			n, err := r.Read(b)
			if n < 0 {
				panic("sftp.file: reader returned negative count from Read")
			}

			if n > 0 {
				read += uint64(n)
				ch := make(chan result)
				id := f.c.nextID()
				worker <- work{id: id, res: ch, off: off}
				filePacket.sendPacket(id, ch, &sshFxpWritePacket{
					ID:     id,
					Handle: f.handle,
					Offset: off,
					Length: uint32(n),
					Data:   b[:n],
				})
				select {
				case <-cancel:
					return
				default:
				}
				off += uint64(n)
			}

			if err != nil {
				if err != io.EOF {
					errCh <- rwErr{off: f.offset, err: err}
				}
				return
			}
		}
	}()
	wg := sync.WaitGroup{}
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for w := range worker {
				res := <-w.res
				close(w.res)
				err := res.err
				if err == nil {
					switch res.typ {
					case sshFxpStatus:
						err = normaliseError(unmarshalStatus(w.id, res.data))
					default:
						err = unimplementedPacketErr(res.typ)
					}
				}
				if err != nil {
					errCh <- rwErr{off: w.off, err: res.err}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	firstErr := rwErr{math.MaxInt64, nil}
	for rwErr := range errCh {
		if rwErr.off <= firstErr.off {
			firstErr = rwErr
		}

		select {
		case <-cancel:
		default:
			close(cancel)
		}
	}
	if firstErr.err != nil {
		f.offset = firstErr.off
		return int64(read), firstErr.err
	}
	f.offset += read
	return int64(read), nil
}

func (f *file) writeToConcurrency(w io.Writer, size int64) (int64, error) {

	if size <= int64(f.c.maxPacket) {
		return f.writeTo(w, size, false)
	}

	concurrency64 := size/int64(f.c.maxPacket) + 1
	concurrency := int(min(concurrency64, int64(f.c.maxConcurrentRequests)))

	type writeWork struct {
		b    []byte
		off  uint64
		err  error
		next chan writeWork
	}

	type readWork struct {
		id         uint32
		res        chan result
		off        uint64
		curr, next chan writeWork
	}

	filePacket := &sftpFilePacket{
		c:        f.c,
		inflight: make(map[uint32]chan result),
	}
	cancel := make(chan struct{})
	worker := make(chan readWork, concurrency)
	readCh := make(chan writeWork)
	go filePacket.recvPacket()
	go func() {
		defer close(worker)
		off := uint64(f.offset)
		cur := readCh
		for {
			next := make(chan writeWork)
			ch := make(chan result)
			id := f.c.nextID()
			readWork := readWork{
				id:   id,
				res:  ch,
				off:  off,
				curr: cur,
				next: next,
			}
			worker <- readWork
			filePacket.sendPacket(id, ch, &sshFxpReadPacket{
				ID:     id,
				Handle: f.handle,
				Offset: off,
				Len:    f.c.maxPacket,
			})
			select {
			case <-cancel:
				return
			default:
			}
			off += uint64(f.c.maxPacket)
			cur = next
		}
	}()
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for w := range worker {
				var readData []byte
				var err error
				res := <-w.res
				close(w.res)
				err = res.err
				if err == nil {
					switch res.typ {
					case sshFxpStatus:
						err = normaliseError(unmarshalStatus(w.id, res.data))
					case sshFxpData:
						sid, data := unmarshalUint32(res.data)
						if sid != w.id {
							err = &unexpectedIDErr{w.id, sid}
						} else {
							l, data := unmarshalUint32(data)
							readData = data[:l]
						}
					default:
						err = unimplementedPacketErr(res.typ)
					}
				}

				writeWork := writeWork{
					b:    readData,
					off:  w.off,
					err:  err,
					next: w.next,
				}

				select {
				case w.curr <- writeWork:
				case <-cancel:
					return
				}
			}
		}()
	}
	defer func() {
		close(cancel)
		// defer to close the recvPacket routine
		f.c.sendPacket(&sshFxpReadPacket{
			ID:     f.c.nextID(),
			Handle: f.handle,
			Offset: 0,
			Len:    0,
		})
		wg.Wait()
	}()
	var (
		err     error
		written int64
		ok      bool
		packet  writeWork
	)
	for {
		packet, ok = <-readCh
		if !ok {
			return written, fmt.Errorf("sftp.File.WriteTo: unexpectedly closed channel")
		}
		err = packet.err
		f.offset += uint64(len(packet.b))

		if len(packet.b) > 0 {
			var n int
			n, err = w.Write(packet.b)
			written += int64(n)
		}
		if err != nil {
			break
		}
		readCh = packet.next
	}
	if err == io.EOF {
		err = nil
	}
	return written, err
}
