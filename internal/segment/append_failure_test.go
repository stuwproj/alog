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

package segment

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

const faultyStride = 4

var errInjected = errors.New("injected failure")

// faultyFile injects failures into an otherwise working file.
type faultyFile struct {
	storage.File
	writeErr     error
	eofErr       error
	partial      int
	syncFailures int
}

func (f *faultyFile) WritePartsAt(parts [][]byte, off int64) (int, error) {
	if f.writeErr == nil {
		return f.File.WritePartsAt(parts, off)
	}

	var all []byte
	for _, p := range parts {
		all = append(all, p...)
	}

	n := min(f.partial, len(all))
	if n > 0 {
		if _, err := f.File.WriteAt(all[:n], off); err != nil {
			return 0, err
		}
	}

	return n, f.writeErr
}

func (f *faultyFile) WriteAt(b []byte, off int64) (int, error) {
	if f.eofErr != nil {
		return 0, f.eofErr
	}

	return f.File.WriteAt(b, off)
}

func (f *faultyFile) Sync() error {
	if f.syncFailures > 0 {
		f.syncFailures--
		return errInjected
	}

	return f.File.Sync()
}

func newFaultySegment(t *testing.T, syncOnAppend bool) (*Segment, *faultyFile, *[]string) {
	t.Helper()

	loader := NewLoader(NewFactory(4096, syncOnAppend, faultyStride), t.TempDir(), 0, storage.NewOSFileSystem(false))

	s, err := loader.Create(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	f := &faultyFile{File: s.file}
	s.file = f

	logs := &[]string{}
	s.logger = func(msg string) { *logs = append(*logs, msg) }

	return s, f, logs
}

func appendRecords(t *testing.T, s *Segment, from, to int) {
	t.Helper()

	for i := from; i < to; i++ {
		_, err := s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
		require.NoError(t, err)
	}
}

// segmentState captures everything a failed append must leave untouched.
type segmentState struct {
	entries any
	offset  int64
	next    uint32
	crc     uint32
	left    uint16
}

func stateOf(s *Segment) segmentState {
	st := s.tracker.State()

	return segmentState{
		offset:  s.writeOffset,
		next:    s.nextWriteIndex,
		crc:     st.CRC,
		left:    st.Remaining,
		entries: s.offsetIndex.Entries(),
	}
}

// nextBatch is a batch of three records that follows r0 and r1, and with the
// stride of 4 completes the stride.
func nextBatch() []frame.MultiPartPayload {
	return []frame.MultiPartPayload{
		frame.NewPayload([]byte("r2")),
		frame.NewPayload([]byte("r3")),
		frame.NewPayload([]byte("r4")),
	}
}

func TestSegment_AppendBatch(t *testing.T) {
	t.Parallel()

	t.Run("failed write is rolled back and the end marker restored", func(t *testing.T) {
		t.Parallel()

		s, f, logs := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)
		before := stateOf(s)

		// 3 more records would cross the stride, so the batch also holds a checkpoint
		f.writeErr = errInjected
		f.partial = 60

		written, err := s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, errInjected)
		require.NotErrorIs(t, err, ErrFailed)
		require.Zero(t, written)
		require.Equal(t, before, stateOf(s), "in-memory state must be rolled back")

		// the file ends at the old write offset again, the partial batch is not visible
		l := layoutOf(t, s)
		require.Equal(t, s.writeOffset, l.eof)
		require.Len(t, l.records, 2)

		// appending continues with the restored tracker: its checkpoint must validate
		f.writeErr = nil
		appendRecords(t, s, 2, 7)

		reinit(t, s, logs, false)
		require.Equal(t, records(7), readRecords(t, s))
	})

	t.Run("does not keep the payloads of the caller", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)

		_, err := s.AppendBatch(nextBatch())
		require.NoError(t, err)

		require.NotEmpty(t, s.bufs)

		for i, buf := range s.bufs {
			require.Nil(t, buf, "buffer %d is still referenced", i)
		}
	})

	t.Run("an empty batch changes nothing", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 3)
		before := stateOf(s)

		written, err := s.AppendBatch(nil)
		require.NoError(t, err)
		require.Zero(t, written)
		require.Equal(t, before, stateOf(s))
		require.Equal(t, uint64(3), s.NextSequence())
	})

	t.Run("a rolled back batch leaves nothing in the index", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)

		// the batch completes the stride, so it holds a checkpoint
		f.writeErr = errInjected
		f.partial = 60

		_, err := s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, errInjected)

		snapshot := s.offsetIndex.Snapshot()
		for frameIdx := range uint32(10) {
			_, _, found := snapshot.ForFrameIndex(frameIdx)
			require.False(t, found, "frame index %d", frameIdx)
		}

		// the entry appears when a batch is published
		f.writeErr = nil
		appendRecords(t, s, 2, 7)

		_, offset, found := s.offsetIndex.Snapshot().ForFrameIndex(4)
		require.True(t, found)
		require.Less(t, offset, s.Published())
	})

	t.Run("failed sync is rolled back and the batch never appears", func(t *testing.T) {
		t.Parallel()

		s, f, logs := newFaultySegment(t, true)
		appendRecords(t, s, 0, 2)
		before := stateOf(s)

		// the data is fully written, only the sync fails
		f.syncFailures = 1

		_, err := s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, errInjected)
		require.NotErrorIs(t, err, ErrFailed)
		require.Equal(t, before, stateOf(s))

		l := layoutOf(t, s)
		require.Equal(t, s.writeOffset, l.eof)
		require.Len(t, l.records, 2, "the unconfirmed batch must not be readable")

		appendRecords(t, s, 2, 6)

		reinit(t, s, logs, false)
		require.Equal(t, records(6), readRecords(t, s))
	})

	t.Run("failure before writing leaves the file untouched", func(t *testing.T) {
		t.Parallel()

		s, _, logs := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)

		raw, err := os.ReadFile(s.Name())
		require.NoError(t, err)

		before := stateOf(s)

		// more vectors than a single writev takes
		batch := make([]frame.MultiPartPayload, 600)
		for i := range batch {
			batch[i] = frame.NewPayload([]byte("x"))
		}

		_, err = s.AppendBatch(batch)
		require.ErrorIs(t, err, frame.ErrBatchTooLarge)
		require.Equal(t, before, stateOf(s))

		after, err := os.ReadFile(s.Name())
		require.NoError(t, err)
		require.Equal(t, raw, after)

		appendRecords(t, s, 2, 6)

		reinit(t, s, logs, false)
		require.Equal(t, records(6), readRecords(t, s))
	})

	t.Run("fails the segment when the end marker cannot be restored", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)

		f.writeErr = errInjected
		f.partial = 20
		f.eofErr = errors.New("restore failed")

		_, err := s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, ErrFailed)
		require.ErrorIs(t, err, errInjected, "the original error stays visible")
		require.ErrorContains(t, err, "restore failed")

		// even though the disk works again
		f.writeErr = nil
		f.eofErr = nil

		_, err = s.AppendOne([]byte("again"))
		require.ErrorIs(t, err, ErrFailed)
	})

	t.Run("fails the segment when syncing the restored end marker fails", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, true)
		appendRecords(t, s, 0, 2)

		// the append sync and the sync of the restored end marker both fail
		f.syncFailures = 2

		_, err := s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, ErrFailed)
		require.ErrorIs(t, err, errInjected)
	})

	t.Run("a failed segment recovers when reopened and keeps confirmed records", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		factory := NewFactory(4096, false, faultyStride)
		loader := NewLoader(factory, dir, 0, storage.NewOSFileSystem(false))

		s, err := loader.Create(0)
		require.NoError(t, err)

		f := &faultyFile{File: s.file}
		s.file = f
		appendRecords(t, s, 0, 2)

		// the first frame of the batch reaches the file, the end marker is not restored
		f.writeErr = errInjected
		f.partial = frame.HeaderSize + 2 + 5
		f.eofErr = errInjected

		_, err = s.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte("r2")),
			frame.NewPayload([]byte("r3")),
		})
		require.ErrorIs(t, err, ErrFailed)
		require.NoError(t, s.Close())

		segments, err := NewLoader(factory, dir, 0, storage.NewOSFileSystem(false)).Load()
		require.NoError(t, err)
		require.Len(t, segments, 1)
		t.Cleanup(func() { _ = segments[0].Close() })

		require.Equal(t, uint64(2), segments[0].NextSequence())
		require.Equal(t, records(2), readRecords(t, segments[0]))

		appendRecords(t, segments[0], 2, 4)
		require.Equal(t, records(4), readRecords(t, segments[0]))
	})
}
