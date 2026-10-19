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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/scanner"
	"github.com/stretchr/testify/require"
)

func TestChain_NewChain(t *testing.T) {
	t.Parallel()

	t.Run("fails when existing data exceeds budget", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		s := CreateSegmentAt(t, dir, 0)
		_, err := s.AppendOne([]byte("payload"))
		require.NoError(t, err)
		require.NoError(t, s.Close())

		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		_, err = NewChain(1024, header.HeaderSize+1, 0, loader)
		require.ErrorIs(t, err, ErrDiskFull)
	})

	t.Run("accepts unbounded segments without count limit", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		chain, err := NewChain(0, 0, 0, loader)
		require.NoError(t, err)
		require.NoError(t, chain.Close())
	})

	t.Run("returns error for non contiguous segments", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		s0 := CreateSegmentAt(t, dir, 0)
		_, err := s0.AppendBatch([]frame.MultiPartPayload{frame.NewPayload([]byte("a")), frame.NewPayload([]byte("b"))})
		require.NoError(t, err)
		require.NoError(t, s0.Close())

		s10 := CreateSegmentAt(t, dir, 10)
		_, err = s10.AppendOne([]byte("z"))
		require.NoError(t, err)
		require.NoError(t, s10.Close())

		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		_, err = NewChain(1024, 0, 0, loader)
		require.Error(t, err)
		require.ErrorContains(t, err, "segments are not contiguous")
	})
}

func TestChain_AppendOne(t *testing.T) {
	t.Parallel()

	chain := NewLoadedChain(t, 2, 1024)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	seq0, err := chain.AppendOne([]byte("a"))
	require.NoError(t, err)
	require.Equal(t, uint64(0), seq0)

	seq1, err := chain.AppendOne([]byte("b"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq1)

	require.Equal(t, int64(header.HeaderSize)+2*int64(frame.HeaderSize+1), chain.LogicalSize())
}

func TestChain_AppendOneSegmentTooLarge(t *testing.T) {
	t.Parallel()

	chain := NewLoadedChain(t, 1, 64)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	_, err := chain.AppendOne(make([]byte, 256))
	require.ErrorIs(t, err, ErrSegmentFull)
	require.ErrorIs(t, err, ErrBatchTooLarge)
}

func TestChain_DiskBudget(t *testing.T) {
	t.Parallel()

	// room for exactly one record: segment header, frame header, payload and end marker
	chain := NewLoadedChainWithDiskBudgetAndPrealloc(t, -1, 0, 1024, header.HeaderSize+2*frame.HeaderSize+1)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	_, err := chain.AppendOne([]byte("a"))
	require.NoError(t, err)

	_, err = chain.AppendOne([]byte("b"))
	require.ErrorIs(t, err, ErrDiskFull)
}

func TestChain_TruncateBefore(t *testing.T) {
	t.Parallel()

	chain := NewLoadedChain(t, 1, 1024)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	_, err := chain.AppendBatch([]frame.MultiPartPayload{
		frame.NewPayload([]byte("a")),
		frame.NewPayload([]byte("b")),
		frame.NewPayload([]byte("c")),
	})
	require.NoError(t, err)

	require.NoError(t, chain.TruncateBefore(chain.Tail().NextSequence()))

	sc, err := chain.Head().NewScanner(^uint32(0), true)
	require.NoError(t, err)
	require.False(t, sc.Scan())
	require.ErrorIs(t, sc.Err(), io.EOF)
}

func TestChain_TruncateAfter(t *testing.T) {
	t.Parallel()

	chain := NewLoadedChain(t, 1, 1024)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	_, err := chain.AppendBatch([]frame.MultiPartPayload{
		frame.NewPayload([]byte("a")),
		frame.NewPayload([]byte("b")),
		frame.NewPayload([]byte("c")),
	})
	require.NoError(t, err)

	require.NoError(t, chain.TruncateAfter(1))

	sc, err := chain.Head().NewScanner(^uint32(0), true)
	require.NoError(t, err)
	require.True(t, scanRecord(sc))
	require.Equal(t, []byte("a"), sc.Frame.Payload)
	require.True(t, scanRecord(sc))
	require.Equal(t, []byte("b"), sc.Frame.Payload)
	require.False(t, scanRecord(sc))
	require.ErrorIs(t, sc.Err(), io.EOF)

	t.Run("a failed truncation fails the chain", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 1, 1024)
		t.Cleanup(func() { _ = chain.Close() })

		for _, p := range []string{"a", "b", "c", "d"} {
			_, err := chain.AppendOne([]byte(p))
			require.NoError(t, err)
		}

		tail := chain.Tail()
		f := &faultyFile{File: tail.file, eofErr: errInjected}
		tail.file = f

		err := chain.TruncateAfter(1)
		require.ErrorIs(t, err, errInjected)
		require.ErrorIs(t, err, ErrFailed)

		_, err = chain.AppendOne([]byte("e"))
		require.ErrorIs(t, err, ErrFailed)
		require.ErrorIs(t, chain.TruncateAfter(0), ErrFailed)
		require.ErrorIs(t, chain.TruncateBefore(1), ErrFailed)
	})
}

func TestChain_Close(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
	chain, err := NewChain(1024, 0, 2, loader)
	require.NoError(t, err)
	require.NoError(t, chain.Close())

	entries, err := os.ReadDir(filepath.Clean(dir))
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}

// scanRecord advances to the next data frame, skipping checkpoints.
func scanRecord(sc *scanner.Scanner) bool {
	for sc.Scan() {
		if !sc.Header.IsCheckpoint() {
			return true
		}
	}

	return false
}

func TestChain_AppendBatch(t *testing.T) {
	t.Parallel()

	t.Run("a failed segment fails every following append", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 0, 1<<20)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		_, err := chain.AppendOne([]byte("a"))
		require.NoError(t, err)

		tail := chain.Tail()
		f := &faultyFile{File: tail.file, writeErr: errInjected, partial: 10, eofErr: errInjected}
		tail.file = f

		disk := chain.DiskSize()
		logical := chain.LogicalSize()

		_, err = chain.AppendOne([]byte("b"))
		require.ErrorIs(t, err, ErrFailed)

		// the disk works again, but the state on disk is unknown until reopened
		f.writeErr = nil
		f.eofErr = nil

		_, err = chain.AppendOne([]byte("c"))
		require.ErrorIs(t, err, ErrFailed)

		_, err = chain.AppendBatch([]frame.MultiPartPayload{frame.NewPayload([]byte("d"))})
		require.ErrorIs(t, err, ErrFailed)

		require.Equal(t, disk, chain.DiskSize())
		require.Equal(t, logical, chain.LogicalSize())
	})

	t.Run("a failed segment fails appends that would rotate to a new segment", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 0, 256)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		_, err := chain.AppendOne([]byte("a"))
		require.NoError(t, err)

		tail := chain.Tail()
		tail.file = &faultyFile{File: tail.file, writeErr: errInjected, partial: 10, eofErr: errInjected}

		_, err = chain.AppendOne([]byte("b"))
		require.ErrorIs(t, err, ErrFailed)

		segments := chain.Segments().Len()

		// far more than fits in the tail, so it goes to a new, perfectly healthy segment
		batch := make([]frame.MultiPartPayload, 300)
		for i := range batch {
			batch[i] = frame.NewPayload([]byte("x"))
		}

		_, err = chain.AppendBatch(batch)
		require.ErrorIs(t, err, ErrFailed)
		require.Equal(t, segments, chain.Segments().Len(), "no segment may be created for a failed chain")
	})

	t.Run("segment files never exceed the maximum size", func(t *testing.T) {
		t.Parallel()

		// no preallocation, so every file is exactly as long as its content
		chain := NewLoadedChainWithDiskBudgetAndPrealloc(t, -1, 0, 300, 0)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		const total = 40

		for i := range total {
			_, err := chain.AppendOne([]byte(fmt.Sprintf("r%02d", i)))
			require.NoError(t, err)
		}

		segments := chain.Segments().Slice()
		require.Greater(t, len(segments), 1)

		records := 0

		for _, seg := range segments {
			info, err := os.Stat(seg.Name())
			require.NoError(t, err)
			require.LessOrEqual(t, info.Size(), int64(300), seg.Name())

			sc, err := seg.NewScanner(1<<20, true)
			require.NoError(t, err)

			for scanRecord(sc) {
				records++
			}
		}

		require.Equal(t, total, records)
	})

	t.Run("a batch that does not fit an empty segment is rejected", func(t *testing.T) {
		t.Parallel()

		// 72 byte header and 16 byte end marker leave room for 40 bytes of frames
		chain := NewLoadedChainWithDiskBudgetAndPrealloc(t, -1, 0, 128, 0)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		_, err := chain.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("b")),
			frame.NewPayload([]byte("c")),
		})
		require.ErrorIs(t, err, ErrSegmentFull)
		require.ErrorIs(t, err, ErrBatchTooLarge)
		require.Equal(t, 1, chain.Segments().Len())
		require.Equal(t, uint64(0), chain.Tail().NextSequence())
	})

	t.Run("a batch that exactly fits an empty segment is accepted", func(t *testing.T) {
		t.Parallel()

		// header, two frames of 17 bytes and the end marker
		const size = header.HeaderSize + 2*(frame.HeaderSize+1) + frame.HeaderSize

		chain := NewLoadedChainWithDiskBudgetAndPrealloc(t, -1, 0, size, 0)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		batch := []frame.MultiPartPayload{frame.NewPayload([]byte("a")), frame.NewPayload([]byte("b"))}

		_, err := chain.AppendBatch(batch)
		require.NoError(t, err)

		_, err = chain.AppendBatch(batch)
		require.NoError(t, err)
		require.Equal(t, 2, chain.Segments().Len())

		for _, seg := range chain.Segments().Slice() {
			info, statErr := os.Stat(seg.Name())
			require.NoError(t, statErr)
			require.Equal(t, int64(size), info.Size())
		}

		// one byte less and it no longer fits
		small := NewLoadedChainWithDiskBudgetAndPrealloc(t, -1, 0, size-1, 0)
		t.Cleanup(func() { require.NoError(t, small.Close()) })

		_, err = small.AppendBatch(batch)
		require.ErrorIs(t, err, ErrBatchTooLarge)
	})

	t.Run("an empty segment must also have room for the checkpoint of the batch", func(t *testing.T) {
		t.Parallel()

		// the batch completes the stride, so a checkpoint follows its frames
		const size = header.HeaderSize + 2*(frame.HeaderSize+1) + 2*frame.HeaderSize

		batch := []frame.MultiPartPayload{frame.NewPayload([]byte("a")), frame.NewPayload([]byte("b"))}

		fits := NewLoadedChainWithDiskBudgetAndFactory(t, NewFactory(-1, false, 2), 0, size, 0)
		t.Cleanup(func() { require.NoError(t, fits.Close()) })

		_, err := fits.AppendBatch(batch)
		require.NoError(t, err)

		info, err := os.Stat(fits.Tail().Name())
		require.NoError(t, err)
		require.Equal(t, int64(size), info.Size())

		short := NewLoadedChainWithDiskBudgetAndFactory(t, NewFactory(-1, false, 2), 0, size-1, 0)
		t.Cleanup(func() { require.NoError(t, short.Close()) })

		_, err = short.AppendBatch(batch)
		require.ErrorIs(t, err, ErrBatchTooLarge)
	})

	t.Run("a rolled back failure does not fail the chain", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 0, 1<<20)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		seq, err := chain.AppendOne([]byte("a"))
		require.NoError(t, err)
		require.Equal(t, uint64(0), seq)

		tail := chain.Tail()
		f := &faultyFile{File: tail.file, writeErr: errInjected, partial: 10}
		tail.file = f

		_, err = chain.AppendOne([]byte("b"))
		require.ErrorIs(t, err, errInjected)
		require.NotErrorIs(t, err, ErrFailed)

		f.writeErr = nil

		seq, err = chain.AppendOne([]byte("b"))
		require.NoError(t, err)
		require.Equal(t, uint64(1), seq, "the failed append must not consume a sequence number")
	})
}
