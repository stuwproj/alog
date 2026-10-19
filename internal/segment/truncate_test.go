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
	"io"
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"github.com/stretchr/testify/require"
)

// FuzzSegment_TailTruncationRecovery truncates persisted tails and verifies
// reload behavior remains safe and bounded.
func FuzzSegment_TailTruncationRecovery(f *testing.F) {
	f.Add(uint8(8), uint16(0))
	f.Add(uint8(8), uint16(7))

	f.Fuzz(func(t *testing.T, nFrames uint8, cut uint16) {
		// create a bounded segment and then truncate to an arbitrary tail offset
		n := int(nFrames) + 1
		path := filepath.Join(t.TempDir(), segmentFileName(0))

		s, err := create(path, 0, nil, false)
		require.NoError(t, err)
		for i := range n {
			_, err = s.AppendOne([]byte{byte(i)})
			require.NoError(t, err)
		}

		require.NoError(t, s.Close())

		st, err := os.Stat(path)
		require.NoError(t, err)
		size := st.Size()

		// normalize cut position to current file size before truncating
		trunc := int64(cut)
		if size > 0 {
			trunc %= size
		}

		require.NoError(t, os.Truncate(path, trunc))

		// reload must either fail safely or recover a coherent sequence window
		loaded, loadErr := load(path, nil, false)
		if loadErr != nil {
			require.True(t,
				errors.Is(loadErr, io.EOF) ||
					errors.Is(loadErr, io.ErrUnexpectedEOF) ||
					errors.Is(loadErr, header.ErrInvalidSegmentHeader) ||
					errors.Is(loadErr, frame.ErrInvalidChecksum) ||
					errors.Is(loadErr, frame.ErrInvalidFrameHeader) ||
					errors.Is(loadErr, frame.ErrInvalidFrameIndex),
			)
			if loaded != nil {
				require.NoError(t, loaded.Close())
			}
			return
		}
		t.Cleanup(func() { require.NoError(t, loaded.Close()) })

		// if the segment loads successfully, the next sequence must be
		// greater than or equal to the start sequence, even if the tail was truncated
		require.GreaterOrEqual(t, loaded.NextSequence(), loaded.StartSequence())

		// successful reloads must pass the same internal frame validation used
		// during load and maintain a bounded number of frames.
		require.NoError(t, loaded.init())
		require.LessOrEqual(t, int(loaded.NextSequence()-loaded.StartSequence()), n)
	})
}

func TestSegment_TruncateAfter(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(10))
	s, err := create(path, 10, nil, false)
	require.NoError(t, err)

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)
	_, err = s.AppendOne([]byte("bb"))
	require.NoError(t, err)
	_, err = s.AppendOne([]byte("ccc"))
	require.NoError(t, err)

	require.Equal(t, 13, int(s.NextSequence()))
	require.NoError(t, s.TruncateAfter(12))
	require.Equal(t, 12, int(s.NextSequence()))

	t.Run("a failed write fails the segment, and scanners stay below the cut", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 8)

		f.eofErr = errInjected

		err := s.TruncateAfter(3)
		require.ErrorIs(t, err, errInjected)
		require.ErrorIs(t, err, ErrFailed)

		_, err = s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, ErrFailed, "an append behind the cut would be lost when loading")
		require.ErrorIs(t, s.TruncateAfter(2), ErrFailed)
		require.ErrorIs(t, s.TruncateBefore(int64(header.HeaderSize), 0), ErrFailed)

		require.Equal(t, records(3), readRecords(t, s))
	})

	t.Run("a failed sync fails the segment, and loading it again gives the cut", func(t *testing.T) {
		t.Parallel()

		s, f, logs := newFaultySegment(t, false)
		appendRecords(t, s, 0, 8)

		f.syncFailures = 1

		err := s.TruncateAfter(3)
		require.ErrorIs(t, err, errInjected)
		require.ErrorIs(t, err, ErrFailed)

		_, err = s.AppendBatch(nextBatch())
		require.ErrorIs(t, err, ErrFailed)

		// the cut reached the file, so that is what the next load sees
		reinit(t, s, logs, false)
		require.Equal(t, records(3), readRecords(t, s))
	})

	t.Run("a cut inside a batch that is torn leaves a log that recovers", func(t *testing.T) {
		t.Parallel()

		// The cut is a checkpoint and an end marker in 1 write. A batch has 3 records, so
		// cutting after the 5th record is inside the second batch.
		for _, tc := range []struct {
			name    string
			written int
			want    int
		}{
			{"nothing reached the file: the truncation did not happen", 0, 6},
			{"part of the checkpoint: back to the end of the first batch", 8, 3},
			{"the checkpoint, and part of the end marker: the cut", 16, 5},
			{"the checkpoint, and most of the end marker: the cut", 28, 5},
			{"everything: the cut", 32, 5},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				s, f, logs := newFaultySegment(t, false)
				_, err := s.AppendBatch(nextBatch())
				require.NoError(t, err)
				_, err = s.AppendBatch(nextBatch())
				require.NoError(t, err)

				before := readRecords(t, s)
				require.Len(t, before, 6)

				s.file = &tornWriteFile{faultyFile: f, written: tc.written}

				require.Error(t, s.TruncateAfter(5))

				reinit(t, s, logs, tc.written != 0 && tc.written != 32)
				require.Equal(t, before[:tc.want], readRecords(t, s))
			})
		}
	})

	t.Run("a truncation that is rejected before anything is written does not fail the segment", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 8)

		require.NoError(t, s.TruncateAfter(100), "beyond the end is nothing to do")

		appendRecords(t, s, 8, 10)
		require.Equal(t, records(10), readRecords(t, s))
	})
}

func TestSegment_TruncateBefore(t *testing.T) {
	t.Parallel()

	t.Run("persists read frontier across Load", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), segmentFileName(10))
		s, err := create(path, 10, nil, false)
		require.NoError(t, err)

		_, err = s.AppendOne([]byte("a"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("b"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("c"))
		require.NoError(t, err)

		physical := int64(header.HeaderSize)
		require.NoError(t, s.TruncateBefore(physical, 10))
		require.Equal(t, physical, s.ReadStart())
		require.Equal(t, uint64(10), s.StartSequence())
	})

	t.Run("rejects invalid offset without persisting", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), segmentFileName(10))
		s, err := create(path, 10, nil, false)
		require.NoError(t, err)

		_, err = s.AppendOne([]byte("a"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("b"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("c"))
		require.NoError(t, err)

		invalid := s.WriteOffset() + 1

		err = s.TruncateBefore(invalid, 12)
		require.Error(t, err)
		require.ErrorIs(t, err, header.ErrInvalidSegmentHeader)
		require.Equal(t, int64(header.HeaderSize), s.ReadStart())
		require.Equal(t, uint64(10), s.StartSequence())
	})

	t.Run("accepts exact bounds and rejects out of bounds", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), segmentFileName(10))
		s, err := create(path, 10, nil, false)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })

		_, err = s.AppendOne([]byte("a"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("b"))
		require.NoError(t, err)
		_, err = s.AppendOne([]byte("c"))
		require.NoError(t, err)

		writeOffset := s.WriteOffset()

		require.NoError(t, s.TruncateBefore(int64(header.HeaderSize), 10))
		require.Equal(t, uint64(10), s.StartSequence())

		require.NoError(t, s.TruncateBefore(writeOffset, s.NextSequence()))
		require.Equal(t, writeOffset, s.ReadStart())

		err = s.TruncateBefore(int64(header.HeaderSize)-1, 10)
		require.ErrorIs(t, err, header.ErrInvalidSegmentHeader)

		err = s.TruncateBefore(writeOffset+1, s.NextSequence())
		require.ErrorIs(t, err, header.ErrInvalidSegmentHeader)
	})
}

// tornWriteFile lets only the first bytes of the cut that a truncation writes reach the file,
// like a crash in the middle of a write.
type tornWriteFile struct {
	*faultyFile
	written int
	hit     bool
}

func (f *tornWriteFile) WriteAt(b []byte, off int64) (int, error) {
	if f.hit || len(b) != 2*frame.HeaderSize {
		return f.faultyFile.WriteAt(b, off)
	}

	f.hit = true

	n, err := f.File.WriteAt(b[:f.written], off)
	if err != nil {
		return n, err
	}

	return n, errInjected
}
