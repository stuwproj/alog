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
	"io"
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"github.com/stretchr/testify/require"
)

func TestSegment_ProjectedSize(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(0))
	s, err := create(path, 0, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	size, err := s.ProjectedSize(frame.NewPayload([]byte("a")), frame.NewPayload([]byte("bb")))
	require.NoError(t, err)
	appendSize, err := s.AppendSize(frame.NewPayload([]byte("a")), frame.NewPayload([]byte("bb")))
	require.NoError(t, err)
	want := s.WriteOffset() + appendSize
	require.Equal(t, want, size)

	// the two frames and the end marker that follows them
	require.Equal(t, int64(header.HeaderSize+2*frame.HeaderSize+3+frame.HeaderSize), size)
}

func TestSegment_AppendSize(t *testing.T) {
	t.Parallel()

	t.Run("counts the frames and the end marker in bytes", func(t *testing.T) {
		t.Parallel()

		s, _ := newBatchTestSegment(t, 100, 0, 0)

		n, err := s.AppendSize(frame.NewPayload([]byte("a")))
		require.NoError(t, err)
		require.Equal(t, int64(frame.HeaderSize+1+frame.HeaderSize), n)

		n, err = s.AppendSize(frame.NewPayload([]byte("a")), frame.NewPayload([]byte("bb")))
		require.NoError(t, err)
		require.Equal(t, int64(2*frame.HeaderSize+3+frame.HeaderSize), n)

		// multiple parts of one record are a single frame
		n, err = s.AppendSize(frame.NewPayload([]byte("a"), []byte("bb")))
		require.NoError(t, err)
		require.Equal(t, int64(frame.HeaderSize+3+frame.HeaderSize), n)
	})

	t.Run("includes the checkpoint when the batch completes the stride", func(t *testing.T) {
		t.Parallel()

		s, _ := newBatchTestSegment(t, 2, 0, 0)

		n, err := s.AppendSize(frame.NewPayload([]byte("a")))
		require.NoError(t, err)
		require.Equal(t, int64(frame.HeaderSize+1+frame.HeaderSize), n)

		n, err = s.AppendSize(frame.NewPayload([]byte("a")), frame.NewPayload([]byte("b")))
		require.NoError(t, err)
		require.Equal(t, int64(2*(frame.HeaderSize+1)+frame.HeaderSize+frame.HeaderSize), n)
	})

	t.Run("matches what an append writes", func(t *testing.T) {
		t.Parallel()

		s, _ := newBatchTestSegment(t, 3, 0, 0)

		for count := 1; count <= 7; count++ {
			batch := make([]frame.MultiPartPayload, count)
			for i := range batch {
				batch[i] = frame.NewPayload([]byte("xyz"))
			}

			predicted, err := s.AppendSize(batch...)
			require.NoError(t, err)

			before := s.WriteOffset()
			_, err = s.AppendBatch(batch)
			require.NoError(t, err)

			// the end marker follows the new write offset, and is part of the size
			require.Equal(t, predicted, s.WriteOffset()-before+frame.HeaderSize, "batch of %d", count)
			require.Equal(t, s.WriteOffset(), layoutOf(t, s).eof)
		}
	})
}

func TestSegment_SequenceAccessors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(10))
	s, err := create(path, 10, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.Equal(t, uint64(10), s.StartSequence())
	require.Equal(t, uint64(10), s.NextSequence())

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)
	require.Equal(t, uint64(11), s.NextSequence())

	require.NoError(t, s.TruncateBefore(s.WriteOffset(), s.NextSequence()))
	require.Equal(t, s.NextSequence(), s.StartSequence())
}

func TestSegment_Reset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(10))
	s, err := create(path, 10, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)

	t.Run("rejects invalid offsets", func(t *testing.T) {
		err := s.Reset(10, int64(frame.HeaderSize-1))
		require.ErrorIs(t, err, header.ErrInvalidSegmentHeader)

		err = s.Reset(10, s.WriteOffset()+1)
		require.ErrorIs(t, err, header.ErrInvalidSegmentHeader)
	})

	t.Run("updates start sequence and offset", func(t *testing.T) {
		err := s.Reset(12, int64(header.HeaderSize))
		require.NoError(t, err)
		require.Equal(t, uint64(12), s.StartSequence())
		require.Equal(t, int64(header.HeaderSize), s.ReadStart())
	})
}

func TestSegment_NewScannerSeekIndex(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(10))
	s, err := create(path, 10, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)
	_, err = s.AppendOne([]byte("bb"))
	require.NoError(t, err)
	_, err = s.AppendOne([]byte("ccc"))
	require.NoError(t, err)

	t.Run("exclusive tail sequence", func(t *testing.T) {
		sc, err := s.NewScanner(1<<20, true)
		require.NoError(t, err)
		require.ErrorIs(t, sc.SeekIndex(13), io.EOF)
		require.ErrorIs(t, sc.Err(), io.EOF)
		require.Equal(t, s.WriteOffset(), sc.NextOffset)
	})

	t.Run("before start", func(t *testing.T) {
		sc, err := s.NewScanner(1<<20, true)
		require.NoError(t, err)
		err = sc.SeekIndex(9)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("beyond tail", func(t *testing.T) {
		sc, err := s.NewScanner(1<<20, true)
		require.NoError(t, err)
		err = sc.SeekIndex(14)
		require.ErrorIs(t, err, io.EOF)
		require.False(t, sc.Scan())
		require.ErrorIs(t, sc.Err(), io.EOF)
	})
}

func corruptAtOffset(t testing.TB, path string, offset int64, value []byte) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	require.NoError(t, err)

	_, err = file.WriteAt(value, offset)
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
}

func corruptIndexStrideAtOffset(t testing.TB, path string, offset int64) {
	t.Helper()
	corruptAtOffset(t, path, offset+4, []byte{0xFF, 0xFF})
}

func corruptPayloadLengthAtOffset(t testing.TB, path string, offset int64) {
	t.Helper()
	corruptAtOffset(t, path, offset, []byte{0xFF, 0xFF, 0xFF, 0xFF})
}

func appendTornFrame(
	t testing.TB,
	path string,
	stride uint16,
	payload frame.MultiPartPayload,
	cut int,
) {
	t.Helper()

	var fh frame.Header
	fh.Set(0, stride, payload)

	frameBytes := make([]byte, 0, int64(len(fh))+payload.Size())
	frameBytes = append(frameBytes, fh[:]...)
	frameBytes = append(frameBytes, payload.Bytes()...)

	require.Greater(t, cut, 0)
	require.Less(t, cut, len(frameBytes))

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)

	_, err = file.Write(frameBytes[:cut])
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
}
