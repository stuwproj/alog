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
	"sync"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/scanner"
	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

const noLimit = ^uint32(0)

func scanAll(t *testing.T, s *Segment) (n int) {
	t.Helper()

	sc, err := s.NewScanner(noLimit, false)
	require.NoError(t, err)

	for sc.Scan() {
		if !sc.Header.IsCheckpoint() {
			n++
		}
	}

	require.ErrorIs(t, sc.Err(), io.EOF)

	return n
}

func TestSegment_Published(t *testing.T) {
	t.Parallel()

	t.Run("follows the write offset", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		require.Equal(t, s.WriteOffset(), s.Published())

		appendRecords(t, s, 0, 7)
		require.Equal(t, s.WriteOffset(), s.Published())

		require.NoError(t, s.TruncateAfter(2))
		require.Equal(t, s.WriteOffset(), s.Published())
	})

	t.Run("is restored when the segment is loaded", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		loader := NewLoader(NewFactory(4096, false, faultyStride), dir, 0, storage.NewOSFileSystem(false))

		s, err := loader.Create(0)
		require.NoError(t, err)
		appendRecords(t, s, 0, 9)
		want := s.WriteOffset()
		require.NoError(t, s.Close())

		segments, err := loader.Load()
		require.NoError(t, err)
		t.Cleanup(func() { _ = segments[0].Close() })

		require.Equal(t, want, segments[0].Published())
		require.Equal(t, 9, scanAll(t, segments[0]))
	})

	t.Run("does not move when an append fails", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 3)
		before := s.Published()

		f.writeErr = errInjected
		f.partial = 40

		_, err := s.AppendBatch(nextBatch())
		require.Error(t, err)
		require.Equal(t, before, s.Published())

		// the torn bytes are behind the published offset, and the scan ends there
		require.Equal(t, 3, scanAll(t, s))
	})

	t.Run("does not move when the sync of an append fails", func(t *testing.T) {
		t.Parallel()

		s, f, _ := newFaultySegment(t, true)
		appendRecords(t, s, 0, 3)
		before := s.Published()

		f.syncFailures = 1

		_, err := s.AppendBatch(nextBatch())
		require.Error(t, err)
		require.Equal(t, before, s.Published())
		require.Equal(t, 3, scanAll(t, s))
	})
}

func TestSegment_Refresh(t *testing.T) {
	t.Parallel()

	t.Run("a scanner that reached the end sees later appends", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)

		sc, err := s.NewScanner(noLimit, false)
		require.NoError(t, err)

		count := func() (n int) {
			for sc.Scan() {
				if !sc.Header.IsCheckpoint() {
					n++
				}
			}

			return n
		}

		require.Equal(t, 2, count())
		require.False(t, s.Refresh(sc), "nothing was published since")

		appendRecords(t, s, 2, 5)
		require.True(t, s.Refresh(sc))
		require.Equal(t, 3, count())
		require.ErrorIs(t, sc.Err(), io.EOF)
	})

	t.Run("maps again when a segment that is not preallocated grew", func(t *testing.T) {
		t.Parallel()

		s, err := NewLoader(NewFactory(-1, false, 0), t.TempDir(), 0, nil).Create(0)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })

		appendRecords(t, s, 0, 2)

		sc, err := s.NewScanner(noLimit, false)
		require.NoError(t, err)

		count := func() (n int) {
			for sc.Scan() {
				if !sc.Header.IsCheckpoint() {
					n++
				}
			}

			return n
		}

		require.Equal(t, 2, count())

		appendRecords(t, s, 2, 5)
		require.True(t, s.Refresh(sc))
		require.Equal(t, 3, count())
		require.ErrorIs(t, sc.Err(), io.EOF)
		require.Equal(t, 5, scanAll(t, s), "a new scanner reads what was appended after the first mapping")
	})

	t.Run("moves the end back when the segment was truncated", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 8)

		sc, err := s.NewScanner(noLimit, false)
		require.NoError(t, err)
		loaderData := func() int64 { return int64(len(sc.Loader.(*scanner.SliceLoader).Data)) } //nolint:errcheck
		require.Greater(t, loaderData(), s.WriteOffset()-1)

		require.NoError(t, s.TruncateAfter(3)) // 3 becomes the next sequence
		require.False(t, s.Refresh(sc), "the end moved back, not forward")

		n := 0
		for sc.Scan() {
			if !sc.Header.IsCheckpoint() {
				n++
			}
		}

		require.Equal(t, 3, n)
		require.LessOrEqual(t, loaderData(), s.Published())
	})

	t.Run("does not read a batch that is not published", func(t *testing.T) {
		t.Parallel()

		s, _, _ := newFaultySegment(t, false)
		appendRecords(t, s, 0, 2)
		published := s.Published()

		// the batch is on disk, but pretend it is still in flight
		_, err := s.AppendBatch(nextBatch())
		require.NoError(t, err)
		s.published.Store(published)

		require.Equal(t, 2, scanAll(t, s))
	})
}

func TestSegment_PublishedConcurrentScan(t *testing.T) {
	t.Parallel()

	const (
		batches   = 200
		batchSize = 7
	)

	// the mapping of a segment covers what was allocated, so allocate all of it
	s, err := NewLoader(
		NewFactory(1<<20, false, faultyStride),
		t.TempDir(),
		0,
		storage.NewOSFileSystem(false),
	).Create(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var wg sync.WaitGroup
	done := make(chan struct{})

	// registered last, so it runs first: the appender must be finished before the segment is closed
	t.Cleanup(wg.Wait)

	wg.Go(func() {
		defer close(done)

		for b := range batches {
			batch := make([]frame.MultiPartPayload, batchSize)
			for i := range batch {
				batch[i] = frame.NewPayload([]byte(fmt.Sprintf("b%d-%d", b, i)))
			}

			if _, err := s.AppendBatch(batch); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	})

	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
		}

		sc, err := s.NewScanner(noLimit, false)
		require.NoError(t, err)

		n := 0
		for sc.Scan() {
			if !sc.Header.IsCheckpoint() {
				require.Equal(t, fmt.Sprintf("b%d-%d", n/batchSize, n%batchSize), string(sc.Frame.Payload))
				n++
			}
		}

		require.ErrorIs(t, sc.Err(), io.EOF, "a scan must end cleanly at a batch boundary")
		require.Zero(t, n%batchSize, "a scan must never end inside a batch")
	}

	wg.Wait()
	require.Equal(t, batches*batchSize, scanAll(t, s))
}
