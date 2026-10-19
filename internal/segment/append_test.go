// MIT License
//
// Copyright (C) 2025 John Kleijn
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
//

package segment

import (
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	modframe "stuw.net/frame"
	"github.com/stretchr/testify/require"
)

func TestSegment_Append(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(0))
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(path))
	})

	s, err := create(path, 0, nil, true)
	require.NoError(t, err)

	_, err = s.AppendBatch([]frame.MultiPartPayload{
		frame.NewPayload([]byte("a")),
		frame.NewPayload([]byte("b")),
	})
	require.NoError(t, err)

	require.Equal(t, uint64(2), s.NextSequence())
	require.NoError(t, s.Close())

	loaded, err := load(path, nil, true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, loaded.Close())
	})

	require.Equal(t, path, loaded.Name())
	require.Equal(t, uint64(2), loaded.NextSequence())
}

type memWriteFile struct {
	syncErr  error
	writeErr error
	b        []byte
	off      int64
	syncs    int
}

func (m *memWriteFile) Name() string { return "mem" }

func (m *memWriteFile) Read(p []byte) (int, error) {
	if m.off >= int64(len(m.b)) {
		return 0, io.EOF
	}

	n := copy(p, m.b[m.off:])
	m.off += int64(n)
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func (m *memWriteFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(m.b)) {
		return 0, io.EOF
	}

	n := copy(p, m.b[off:])
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func (m *memWriteFile) ReadFull(p []byte) (int, error) {
	return io.ReadFull(m, p)
}

func (m *memWriteFile) Truncate(size int64) error {
	if size < 0 {
		return io.ErrUnexpectedEOF
	}

	if size <= int64(len(m.b)) {
		m.b = m.b[:size]
	} else {
		m.b = append(m.b, make([]byte, size-int64(len(m.b)))...)
	}

	if m.off > size {
		m.off = size
	}

	return nil
}

func (m *memWriteFile) Close() error { return nil }

func (m *memWriteFile) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = m.off + offset
	case io.SeekEnd:
		next = int64(len(m.b)) + offset
	default:
		return 0, io.ErrUnexpectedEOF
	}

	if next < 0 {
		return 0, io.ErrUnexpectedEOF
	}

	m.off = next
	return m.off, nil
}

func (m *memWriteFile) Write(p []byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	end := m.off + int64(len(p))
	if end > int64(len(m.b)) {
		grow := make([]byte, end-int64(len(m.b)))
		m.b = append(m.b, grow...)
	}

	copy(m.b[m.off:end], p)
	m.off = end

	return len(p), nil
}

func (m *memWriteFile) WritePartsAt(p [][]byte, off int64) (int, error) {
	total := 0
	for _, part := range p {
		n, err := m.WriteAt(part, off)
		if err != nil {
			return total + n, err
		}
		off += int64(n)
		total += n
	}

	return total, nil
}

func (m *memWriteFile) WriteAt(p []byte, off int64) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}

	end := off + int64(len(p))
	if end > int64(len(m.b)) {
		grow := make([]byte, end-int64(len(m.b)))
		m.b = append(m.b, grow...)
	}

	copy(m.b[off:end], p)

	if m.off < end {
		m.off = end
	}

	return len(p), nil
}

func (m *memWriteFile) WriteTo(parts [][]byte) (int, error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	written := 0
	for _, p := range parts {
		n, err := m.Write(p)
		written += n
		if err != nil {
			return written, err
		}
	}

	return written, nil
}

func (m *memWriteFile) Stat() (os.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *memWriteFile) Fd() uintptr {
	return 0
}

func (m *memWriteFile) Sync() error {
	m.syncs++
	return m.syncErr
}

func testMeta(t *testing.T) *header.MetaSlot {
	t.Helper()

	h := header.NewHeader(0, 7)
	m, err := h.Meta()
	require.NoError(t, err)
	return &m
}

func TestSegment_append(t *testing.T) {
	t.Parallel()

	newTestSegment := func(f *memWriteFile, idx *checkpoint.Index, tr *checkpoint.Tracker, m *header.MetaSlot, syncOnAppend bool) *Segment {
		return &Segment{
			file:         f,
			offsetIndex:  idx,
			tracker:      tr,
			meta:         m,
			syncOnAppend: syncOnAppend,
			hdrs:         make([]frame.Header, 16),
			bufs:         make([][]byte, 0),
		}
	}

	t.Run("empty batch", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		frameIdx, written, err := s.append(0, 0, nil)
		require.NoError(t, err)
		require.Equal(t, uint32(0), frameIdx)
		require.Equal(t, int64(0), written)
		require.Empty(t, f.b)
	})

	t.Run("batch with EOF and sync", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), true)

		frameIdx, written, err := s.append(0, 0, []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(2), frameIdx)
		require.Equal(t, int64(2*frame.HeaderSize+3), written)
		require.Equal(t, 1, f.syncs)

		// physical bytes include trailing EOF frame
		require.Equal(t, int(written)+frame.HeaderSize, len(f.b))

		var first frame.Header
		copy(first[:], f.b[:frame.HeaderSize])
		require.NoError(t, first.CheckFlags())
		require.False(t, first.IsBatchEnd())
		require.False(t, first.IsEOF())

		var eof frame.Header
		copy(eof[:], f.b[len(f.b)-frame.HeaderSize:])
		require.True(t, eof.IsEOF())
	})

	t.Run("batch checksum excludes trailing EOF marker", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		_, _, err := s.append(0, 0, []frame.MultiPartPayload{
			frame.NewPayload([]byte("ab")),
			frame.NewPayload([]byte("cde")),
		})
		require.NoError(t, err)

		var first frame.Header
		copy(first[:], f.b[:frame.HeaderSize])
		require.False(t, first.IsBatchEnd())

		frameLen := int(first.PayloadLength())
		require.Equal(t, frame.HeaderSize+frameLen+frame.HeaderSize+int(frame.HeaderSize+3), len(f.b))

		payload := f.b[frame.HeaderSize : frame.HeaderSize+frameLen]
		sum := crc32.Update(0, modframe.CRC32cTable, first[:first.ChecksumBytes()])
		sum = crc32.Update(sum, modframe.CRC32cTable, payload)
		require.Equal(t, first.Checksum(), sum)
	})

	t.Run("every record has its own frame header and the last one ends the batch", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)

		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		records := []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bc")),
			frame.NewPayload([]byte("def")),
		}

		_, n, err := s.append(0, 0, records)
		require.NoError(t, err)

		expectedLen := 0
		expectedLen += len(records) * frame.HeaderSize // physical headers
		expectedLen += 6                               // payload bytes

		require.Equal(t, int64(expectedLen), n)

		// physical bytes include trailing EOF frame
		require.Equal(t, expectedLen+frame.HeaderSize, len(f.b))

		var first frame.Header
		copy(first[:], f.b[:frame.HeaderSize])
		require.False(t, first.IsBatchEnd())

		var eof frame.Header
		copy(eof[:], f.b[len(f.b)-frame.HeaderSize:])
		require.True(t, eof.IsEOF())

		offset := 0
		for i, rec := range records {
			var h frame.Header
			copy(h[:], f.b[offset:offset+frame.HeaderSize])
			require.NoError(t, h.CheckFlags())
			require.Equal(t, i == len(records)-1, h.IsBatchEnd(), "record %d batch end", i)
			require.Equal(t, uint16(1), h.IndexStride())
			require.Equal(t, uint32(rec.Size()), h.PayloadLength(), "record %d payload length", i)

			offset += frame.HeaderSize + int(rec.Size())
		}

		require.Equal(t, expectedLen, offset)
	})

	t.Run("single payload writes physical header and advances index", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		frameIdx, written, err := s.append(3, 10, []frame.MultiPartPayload{frame.NewPayload([]byte("abc"))})
		require.NoError(t, err)
		require.Equal(t, uint32(4), frameIdx)
		require.Equal(t, int64(frame.HeaderSize+3), written)
		eofSize := frame.HeaderSize
		require.Equal(t, 10+int(written)+eofSize, len(f.b))

		var h frame.Header
		copy(h[:], f.b[10:10+frame.HeaderSize])
		require.NoError(t, h.CheckFlags())
		require.True(t, h.IsBatchEnd())
		require.False(t, h.IsEOF())
		require.Equal(t, uint32(3), h.PayloadLength())
		require.Equal(t, uint16(1), h.IndexStride())
	})

	t.Run("checkpoint updates offset index", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(1), testMeta(t), false)

		batch := []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		}

		frameIdx, written, err := s.append(0, 100, batch)
		require.NoError(t, err)
		require.Equal(t, uint32(2), frameIdx)

		_, _, found := idx.ForFrameIndex(frameIdx)
		require.False(t, found, "the checkpoint is indexed when the batch is published")

		s.indexPendingCheckpoint()

		cpIdx, cpOffset, found := idx.ForFrameIndex(frameIdx)
		require.True(t, found)
		require.Equal(t, frameIdx, cpIdx)
		require.Equal(t, int64(100)+written-frame.HeaderSize, cpOffset)
	})

	t.Run("single payload with checkpoint does not panic and sets index", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(1), testMeta(t), false)

		frameIdx, written, err := s.append(0, 50, []frame.MultiPartPayload{frame.NewPayload([]byte("x"))})
		require.NoError(t, err)
		require.Equal(t, uint32(1), frameIdx)
		require.Equal(t, int64(frame.HeaderSize+1+frame.HeaderSize), written)

		s.indexPendingCheckpoint()

		cpIdx, cpOffset, found := idx.ForFrameIndex(frameIdx)
		require.True(t, found)
		require.Equal(t, frameIdx, cpIdx)
		require.Equal(t, int64(50)+written-frame.HeaderSize, cpOffset)
	})

	t.Run("write error is returned", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{writeErr: errors.New("write failure")}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		_, _, err := s.append(0, 0, []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		})
		require.ErrorContains(t, err, "write failure")
	})

	t.Run("sync error is wrapped", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{syncErr: errors.New("sync failure")}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), true)

		_, _, err := s.append(0, 0, []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		})
		require.ErrorContains(t, err, "failed to sync segment file after append")
		require.ErrorContains(t, err, "sync failure")
	})

	t.Run("does not alter prefix before write offset", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{b: []byte("0123456789")}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		_, written, err := s.append(0, 10, []frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		})
		require.NoError(t, err)
		require.Equal(t, "0123456789", string(f.b[:10]))
		eofSize := frame.HeaderSize
		require.Equal(t, 10+int(written)+eofSize, len(f.b))
	})

	t.Run("batch too large returns explicit error", func(t *testing.T) {
		t.Parallel()

		f := &memWriteFile{}
		idx := checkpoint.NewIndex(0)
		s := newTestSegment(f, idx, checkpoint.NewTracker(0), testMeta(t), false)

		batch := make([]frame.MultiPartPayload, int(^uint16(0))+1)
		_, _, err := s.append(0, 0, batch)
		require.ErrorIs(t, err, frame.ErrBatchTooLarge)
	})
}
