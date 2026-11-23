package decrypt

// WAL commit/truncation handling is adapted from freanocank/wechat-decrypt,
// commit f0b3f9f95dda2d954ee3dbe1d139fb1944a5ecfd (MIT, copyright 2026 cocofeng).
// See docs/third-party/wechat-decrypt.LICENSE and docs/wal-snapshots.md.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrSourceChanged   = errors.New("source database/WAL changed during capture")
	ErrInvalidWAL      = errors.New("invalid WAL")
	ErrRollbackJournal = errors.New("rollback journal present; retry after it is closed")
)

// SourceSignature describes the source actually used by a completed capture.
// It is comparable; WALHeader detects generation resets even at equal size.
// Times describe file observation, not an account-wide transaction timestamp.
type SourceSignature struct {
	Size       int64
	MTimeNS    int64
	WALPresent bool
	WALSize    int64
	WALMTimeNS int64
	WALHeader  string
}

type sourceState struct {
	signature SourceSignature
	main, wal os.FileInfo
}

func StatSource(path string) (SourceSignature, error) {
	state, err := statSource(path)
	return state.signature, err
}

func statSource(path string) (sourceState, error) {
	var state sourceState
	var err error
	state.main, err = os.Stat(path)
	if err != nil {
		return state, err
	}
	state.signature.Size = state.main.Size()
	state.signature.MTimeNS = state.main.ModTime().UnixNano()
	if _, err = os.Stat(path + "-journal"); err == nil {
		return state, ErrRollbackJournal
	} else if !os.IsNotExist(err) {
		return state, err
	}
	wal, err := os.Open(path + "-wal")
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer wal.Close()
	state.wal, err = wal.Stat()
	if err != nil {
		return state, err
	}
	state.signature.WALPresent = true
	state.signature.WALSize = state.wal.Size()
	state.signature.WALMTimeNS = state.wal.ModTime().UnixNano()
	header := make([]byte, min(int64(32), state.wal.Size()))
	if _, err = io.ReadFull(wal, header); err != nil {
		return state, ErrSourceChanged
	}
	digest := sha256.Sum256(header)
	state.signature.WALHeader = hex.EncodeToString(digest[:])
	return state, nil
}

func sameSource(a, b sourceState) bool {
	return a.signature == b.signature && os.SameFile(a.main, b.main) &&
		((a.wal == nil && b.wal == nil) || (a.wal != nil && b.wal != nil && os.SameFile(a.wal, b.wal)))
}

type sourceSnapshot struct {
	path      string
	state     sourceState
	main, wal *os.File
	// Only committed WAL offsets are retained, never whole database/page payloads.
	pages                map[uint32]int64
	count, mainAvailable int64
}

func openSourceSnapshot(ctx context.Context, path string) (*sourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := statSource(path)
	if err != nil {
		return nil, err
	}
	s := &sourceSnapshot{path: path, state: state, count: state.signature.Size / PageSize, mainAvailable: state.signature.Size / PageSize}
	fail := func(err error) (*sourceSnapshot, error) { s.close(); return nil, err }
	if state.signature.Size < PageSize || state.signature.Size%PageSize != 0 {
		return fail(ErrInvalidDatabase)
	}
	s.main, err = os.Open(path)
	if os.IsNotExist(err) {
		return fail(ErrSourceChanged)
	}
	if err != nil {
		return fail(err)
	}
	info, err := s.main.Stat()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(state.main, info) || info.Size() != state.signature.Size || info.ModTime().UnixNano() != state.signature.MTimeNS {
		return fail(ErrSourceChanged)
	}
	if state.signature.WALPresent {
		s.wal, err = os.Open(path + "-wal")
		if os.IsNotExist(err) {
			return fail(ErrSourceChanged)
		}
		if err != nil {
			return fail(err)
		}
		info, err = s.wal.Stat()
		if err != nil {
			return fail(err)
		}
		if !os.SameFile(state.wal, info) || info.Size() != state.signature.WALSize || info.ModTime().UnixNano() != state.signature.WALMTimeNS {
			return fail(ErrSourceChanged)
		}
		if err = s.readWAL(ctx); err != nil {
			if stableErr := s.stable(); errors.Is(stableErr, ErrSourceChanged) {
				err = stableErr
			}
			return fail(err)
		}
	}
	if err = s.stable(); err != nil {
		return fail(err)
	}
	return s, nil
}

func (s *sourceSnapshot) close() {
	if s.main != nil {
		_ = s.main.Close()
	}
	if s.wal != nil {
		_ = s.wal.Close()
	}
}
func (s *sourceSnapshot) stable() error {
	now, err := statSource(s.path)
	if os.IsNotExist(err) {
		return ErrSourceChanged
	}
	if err != nil {
		return err
	}
	if !sameSource(s.state, now) {
		return ErrSourceChanged
	}
	return nil
}

func walChecksum(data []byte, order binary.ByteOrder, a, b uint32) (uint32, uint32) {
	for i := 0; i < len(data); i += 8 {
		a += order.Uint32(data[i:]) + b
		b += order.Uint32(data[i+4:]) + a
	}
	return a, b
}

func (s *sourceSnapshot) readWAL(ctx context.Context) error {
	size := s.state.signature.WALSize
	if size == 0 {
		return nil
	}
	if size < 32 {
		return fmt.Errorf("%w: incomplete header", ErrInvalidWAL)
	}
	header := make([]byte, 32)
	if _, err := s.wal.ReadAt(header, 0); err != nil {
		return err
	}
	digest := sha256.Sum256(header)
	if hex.EncodeToString(digest[:]) != s.state.signature.WALHeader {
		return ErrSourceChanged
	}
	magic := binary.BigEndian.Uint32(header)
	var order binary.ByteOrder = binary.LittleEndian
	if magic == 0x377f0683 {
		order = binary.BigEndian
	} else if magic != 0x377f0682 {
		return fmt.Errorf("%w: unsupported magic", ErrInvalidWAL)
	}
	if binary.BigEndian.Uint32(header[4:]) != 3007000 || binary.BigEndian.Uint32(header[8:]) != PageSize {
		return fmt.Errorf("%w: unsupported version or page size", ErrInvalidWAL)
	}
	a, b := walChecksum(header[:24], order, 0, 0)
	if a != binary.BigEndian.Uint32(header[24:]) || b != binary.BigEndian.Uint32(header[28:]) {
		return fmt.Errorf("%w: header checksum", ErrInvalidWAL)
	}
	pending := make(map[uint32]int64)
	s.pages = make(map[uint32]int64)
	frame := make([]byte, 24+PageSize)
	// Ignore an incomplete trailing frame: it cannot commit a transaction.
	for offset := int64(32); offset+int64(len(frame)) <= size; offset += int64(len(frame)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := s.wal.ReadAt(frame, offset); err != nil {
			return err
		}
		// Reset WAL files can retain complete frames of a previous generation.
		if !bytes.Equal(frame[8:16], header[16:24]) {
			break
		}
		page := binary.BigEndian.Uint32(frame)
		commit := binary.BigEndian.Uint32(frame[4:])
		if page == 0 || page == 0xffffffff || commit == 0xffffffff {
			return fmt.Errorf("%w: invalid page or commit size", ErrInvalidWAL)
		}
		a, b = walChecksum(frame[:8], order, a, b)
		a, b = walChecksum(frame[24:], order, a, b)
		if a != binary.BigEndian.Uint32(frame[16:]) || b != binary.BigEndian.Uint32(frame[20:]) {
			return fmt.Errorf("%w: frame checksum", ErrInvalidWAL)
		}
		pending[page] = offset + 24
		if commit != 0 {
			for page, location := range pending {
				s.pages[page] = location
			}
			clear(pending)
			for page := range s.pages {
				if page > commit {
					delete(s.pages, page)
				}
			}
			s.count = int64(commit)
			s.mainAvailable = min(s.mainAvailable, int64(commit))
		}
	}
	if s.count > s.mainAvailable+int64(len(s.pages)) {
		return fmt.Errorf("%w: committed growth has missing pages", ErrInvalidWAL)
	}
	return nil
}

func (s *sourceSnapshot) readPage(page int64, buffer []byte) error {
	if offset, ok := s.pages[uint32(page)]; ok {
		_, err := s.wal.ReadAt(buffer, offset)
		return err
	}
	if page > s.mainAvailable {
		return fmt.Errorf("%w: missing committed page %d", ErrInvalidWAL, page)
	}
	_, err := s.main.ReadAt(buffer, (page-1)*PageSize)
	return err
}
