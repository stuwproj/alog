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

package alog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"stuw.net/alog/internal/frame"
	"github.com/stretchr/testify/require"
)

func TestScanner_SeekSequence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	l, err := New(dir, DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append(
		[]byte("one"),
		[]byte("two"),
		[]byte("three"),
	)
	require.NoError(t, err)

	s, err := NewScanner(l)
	require.NoError(t, err)
	require.NoError(t, s.SeekSequence(1))
	require.True(t, s.Scan())
	require.Equal(t, 1, int(s.LastSequence()))
	require.Equal(t, []byte("two"), s.Data())

	newSeekLog := func(t *testing.T, opts Options, n int) *Log {
		t.Helper()

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for i := range n {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)
		}

		return l
	}

	t.Run("works after the scanner reached the end", func(t *testing.T) {
		t.Parallel()

		l := newSeekLog(t, DefaultOptions(), 5)

		s, err := NewScanner(l)
		require.NoError(t, err)
		scanAllData(t, s)

		require.NoError(t, s.SeekSequence(2))

		seqs, _ := scanAllData(t, s)
		require.Equal(t, []uint64{2, 3, 4}, seqs)
	})

	t.Run("starts a new scan after StopAt ended one", func(t *testing.T) {
		t.Parallel()

		l := newSeekLog(t, DefaultOptions(), 5)

		s, err := NewScanner(l)
		require.NoError(t, err)
		s.StopAt(2)

		seqs, _ := scanAllData(t, s)
		require.Equal(t, []uint64{0, 1}, seqs)

		require.NoError(t, s.SeekSequence(1))
		seqs, _ = scanAllData(t, s)
		require.Equal(t, []uint64{1}, seqs)
	})

	t.Run("returns ErrOutOfBounds at or after the next sequence, and keeps the scanner usable", func(t *testing.T) {
		t.Parallel()

		l := newSeekLog(t, DefaultOptions(), 5)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.True(t, s.Scan())

		require.ErrorIs(t, s.SeekSequence(5), ErrOutOfBounds)
		require.ErrorIs(t, s.SeekSequence(1_000), ErrOutOfBounds)
		require.NoError(t, s.Err())

		seqs, _ := scanAllData(t, s)
		require.Equal(t, []uint64{1, 2, 3, 4}, seqs, "the scanner stayed where it was")
	})

	t.Run("returns ErrOutOfBounds for an empty log", func(t *testing.T) {
		t.Parallel()

		l := newSeekLog(t, DefaultOptions(), 0)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.ErrorIs(t, s.SeekSequence(0), ErrOutOfBounds)
	})

	t.Run("a sequence before the first record starts at the first record", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.StartSequence = 100

		l := newSeekLog(t, opts, 3)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.NoError(t, s.SeekSequence(5))

		seqs, _ := scanAllData(t, s)
		require.Equal(t, []uint64{100, 101, 102}, seqs)
	})

	t.Run("finds records in later segments", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l := newSeekLog(t, opts, 60)
		require.Greater(t, countSegments(t, l), 2)

		for _, seq := range []uint64{0, 16, 17, 18, 40, 59} {
			s, err := NewScanner(l)
			require.NoError(t, err)
			require.NoError(t, s.SeekSequence(seq))
			require.True(t, s.Scan(), "seq %d", seq)
			require.Equal(t, seq, s.LastSequence())
			require.Equal(t, "record-"+strconv.FormatUint(seq, 10), string(s.Data()))
		}
	})

	t.Run("can be used while the head is truncated and the log is appended to", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l := newSeekLog(t, opts, 1)

		var (
			wg   sync.WaitGroup
			stop atomic.Bool
			err  error
		)

		errCh := make(chan error, 3)

		for worker := range 3 {
			wg.Go(func() {
				for i := uint64(0); !stop.Load(); i++ {
					s, scanErr := NewScanner(l)
					if scanErr != nil {
						errCh <- scanErr
						return
					}

					first, next := l.readable()
					seq := first + (i*5+uint64(worker))%(next-first)

					scanErr = s.SeekSequence(seq)
					if errors.Is(scanErr, ErrOutOfBounds) || errors.Is(scanErr, ErrSegmentReaped) {
						continue // the log moved on
					}

					if scanErr != nil {
						errCh <- fmt.Errorf("seek %d: %w", seq, scanErr)
						return
					}

					if !s.Scan() {
						if errors.Is(s.Err(), ErrSegmentReaped) {
							continue
						}

						errCh <- fmt.Errorf("scan from %d: %w", seq, s.Err())
						return
					}

					// a sequence that was truncated away is positioned at the first one that is left
					if got := "record-" + strconv.FormatUint(s.LastSequence(), 10); string(s.Data()) != got {
						errCh <- fmt.Errorf("record %d has payload %q", s.LastSequence(), s.Data())
						return
					}
				}
			})
		}

		for i := 1; i < 400; i++ {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)

			if i%10 == 0 {
				first, _ := l.FirstSequence()
				require.NoError(t, l.TruncateBefore(first+5))
			}
		}

		stop.Store(true)
		wg.Wait()
		close(errCh)

		for err = range errCh {
			require.NoError(t, err)
		}
	})

	t.Run("can be used while the log is appended to", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l := newSeekLog(t, opts, 1)

		var (
			wg   sync.WaitGroup
			stop atomic.Bool
			err  error
		)

		errCh := make(chan error, 3)

		for worker := range 3 {
			wg.Go(func() {
				for i := uint64(0); !stop.Load(); i++ {
					s, scanErr := NewScanner(l)
					if scanErr != nil {
						errCh <- scanErr
						return
					}

					first, next := l.readable()
					seq := first + (i*7+uint64(worker))%(next-first)

					if scanErr = s.SeekSequence(seq); scanErr != nil {
						errCh <- fmt.Errorf("seek %d (%d-%d): %w", seq, first, next, scanErr)
						return
					}

					if !s.Scan() || s.LastSequence() != seq || string(s.Data()) != "record-"+strconv.FormatUint(seq, 10) {
						errCh <- fmt.Errorf("seek %d got %d %q: %w", seq, s.LastSequence(), s.Data(), s.Err())
						return
					}
				}
			})
		}

		for i := 1; i < 400; i++ {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)
		}

		stop.Store(true)
		wg.Wait()
		close(errCh)

		for err = range errCh {
			require.NoError(t, err)
		}
	})
}

func TestScanner_StopAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	// a batch is never split over segments: this one needs 156 bytes
	opts.Storage.MaxSegmentSize = 256

	l, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append([]byte("a"), []byte("b"), []byte("c"), []byte("d"))
	require.NoError(t, err)

	scan := func(t *testing.T, seek, stop uint64) []uint64 {
		t.Helper()

		s, err := NewScanner(l)
		require.NoError(t, err)

		if seek > 0 {
			require.NoError(t, s.SeekSequence(seek))
		}

		s.StopAt(stop)

		seqs, _ := scanAllData(t, s)

		return seqs
	}

	t.Run("the end is exclusive", func(t *testing.T) {
		require.Equal(t, []uint64{0}, scan(t, 0, 1))
		require.Equal(t, []uint64{0, 1}, scan(t, 0, 2))
		require.Equal(t, []uint64{2}, scan(t, 2, 3))
		require.Equal(t, []uint64{1, 2}, scan(t, 1, 3))
	})

	t.Run("an end beyond the tail scans to the tail", func(t *testing.T) {
		require.Equal(t, []uint64{0, 1, 2, 3}, scan(t, 0, 100))
		require.Equal(t, []uint64{0, 1, 2, 3}, scan(t, 0, 4))
	})

	t.Run("no end scans to the tail", func(t *testing.T) {
		require.Equal(t, []uint64{0, 1, 2, 3}, scan(t, 0, 0))
	})

	t.Run("stays stopped", func(t *testing.T) {
		s, err := NewScanner(l)
		require.NoError(t, err)
		s.StopAt(2)

		require.True(t, s.Scan())
		require.True(t, s.Scan())
		require.False(t, s.Scan())
		require.False(t, s.Scan())
		require.NoError(t, s.Err())
	})
}

func TestScanner_Data(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	l, err := New(dir, DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append([]byte("x"), []byte("y"))
	require.NoError(t, err)

	s, err := NewScanner(l)
	require.NoError(t, err)
	require.True(t, s.Scan())
	data := s.Data()
	require.Equal(t, []byte("x"), data)
	require.Equal(t, []byte("x"), s.Data())
}

func TestLog_SeekWithoutOffsetIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	opts.Storage.MaxSegmentSize = 1024

	l, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	// fewer records than the stride, so there are no checkpoints, and nothing is indexed
	_, err = l.Append([]byte("a"), []byte("b"), []byte("c"), []byte("d"))
	require.NoError(t, err)

	s, err := NewScanner(l)
	require.NoError(t, err)
	require.NoError(t, s.SeekSequence(0))
	require.True(t, s.Scan())
	require.Equal(t, uint64(0), s.LastSequence())
	require.Equal(t, []byte("a"), s.Data())
}

func TestScanner_Close(t *testing.T) {
	t.Parallel()

	newLog := func(t *testing.T) *Log {
		t.Helper()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("a"), []byte("b"))
		require.NoError(t, err)

		return l
	}

	t.Run("before end reports ErrClosed", func(t *testing.T) {
		t.Parallel()

		s, err := NewScanner(newLog(t))
		require.NoError(t, err)
		require.True(t, s.Scan())

		require.NoError(t, s.Close())
		require.False(t, s.Scan())
		require.ErrorIs(t, s.Err(), ErrClosed)
		require.ErrorIs(t, s.SeekSequence(0), ErrClosed)
	})

	t.Run("a scanner that is closed at the end does not follow the log anymore", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("a"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)
		scanAllData(t, s)

		require.NoError(t, s.Close())

		_, err = l.Append([]byte("b"))
		require.NoError(t, err)

		require.False(t, s.Scan())
		require.NoError(t, s.Err())
		require.NoError(t, s.SeekSequence(0))
	})

	t.Run("after end keeps nil error", func(t *testing.T) {
		t.Parallel()

		s, err := NewScanner(newLog(t))
		require.NoError(t, err)

		for s.Scan() {
		}

		require.NoError(t, s.Err())
		require.NoError(t, s.Close())
		require.NoError(t, s.Err())
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()

		s, err := NewScanner(newLog(t))
		require.NoError(t, err)
		require.NoError(t, s.Close())
		require.NoError(t, s.Close())
		require.ErrorIs(t, s.Err(), ErrClosed)
	})
}

func TestScanner_Scan(t *testing.T) {
	t.Parallel()

	t.Run("data survives segment close", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)

		_, err = l.Append([]byte("payload"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.True(t, s.Scan())

		data := s.Data()
		require.NoError(t, l.Close())

		// payload is a copy, so it must remain readable after unmap
		require.Equal(t, []byte("payload"), data)
	})

	t.Run("continues into the next segment", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for i := range 40 {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)
		}

		s, err := NewScanner(l)
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Len(t, seqs, 40)
		require.Equal(t, "record-0", data[0])
		require.Equal(t, "record-39", data[39])
	})

	t.Run("returns false at the end, and continues when records are appended", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("a"), []byte("b"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)

		_, data := scanAllData(t, s)
		require.Equal(t, []string{"a", "b"}, data)

		require.False(t, s.Scan())
		require.NoError(t, s.Err())

		_, err = l.Append([]byte("c"))
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Equal(t, []uint64{2}, seqs)
		require.Equal(t, []string{"c"}, data)
	})

	t.Run("follows into segments that are created after it", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("record-0"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)

		seqs, _ := scanAllData(t, s)
		require.Equal(t, []uint64{0}, seqs)

		for i := 1; i < 60; i++ {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)
		}

		seqs, data := scanAllData(t, s)
		require.Len(t, seqs, 59)
		require.Equal(t, uint64(1), seqs[0])
		require.Equal(t, "record-59", data[58])
		require.Greater(t, countSegments(t, l), 2, "the records have to span segments")
	})

	t.Run("keeps up with a writer that rotates segments", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		const total = 500

		var written atomic.Bool

		go func() {
			defer written.Store(true)

			for i := range total {
				if _, appendErr := l.Append([]byte("record-" + strconv.Itoa(i))); appendErr != nil {
					panic(appendErr)
				}
			}
		}()

		s, err := NewScanner(l)
		require.NoError(t, err)

		var seen []uint64

		for len(seen) < total {
			finished := written.Load()

			for s.Scan() {
				seen = append(seen, s.LastSequence())
				require.Equal(t, "record-"+strconv.Itoa(int(s.LastSequence())), string(s.Data()))
			}

			require.NoError(t, s.Err())

			if finished && len(seen) < total && !s.Scan() {
				break
			}
		}

		require.Len(t, seen, total)

		for i, seq := range seen {
			require.Equal(t, uint64(i), seq)
		}
	})

	t.Run("fails with ErrSegmentReaped when its segment was recycled", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512
		opts.Storage.MaxSegments = 2

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("record-0"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.True(t, s.Scan())

		for i := 1; i < 80; i++ {
			_, err = l.Append([]byte("record-" + strconv.Itoa(i)))
			require.NoError(t, err)
		}

		require.False(t, s.Scan())
		require.ErrorIs(t, s.Err(), ErrSegmentReaped)
	})

	t.Run("a truncation behind its position makes it fail, even when the new records look the same", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for i := range 10 {
			_, err = l.Append([]byte(fmt.Sprintf("old-%02d", i)))
			require.NoError(t, err)
		}

		s, err := NewScanner(l)
		require.NoError(t, err)

		for range 6 {
			require.True(t, s.Scan())
		}

		require.NoError(t, l.TruncateAfter(3))

		for i := 4; i < 10; i++ {
			_, err = l.Append([]byte(fmt.Sprintf("new-%02d", i)))
			require.NoError(t, err)
		}

		require.False(t, s.Scan())
		require.ErrorIs(t, s.Err(), ErrSegmentReaped)
	})

	t.Run("a scanner before the cut keeps going and sees the new records", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for i := range 10 {
			_, err = l.Append([]byte(fmt.Sprintf("old-%02d", i)))
			require.NoError(t, err)
		}

		s, err := NewScanner(l)
		require.NoError(t, err)

		for range 2 {
			require.True(t, s.Scan())
		}

		require.NoError(t, l.TruncateAfter(3))

		_, err = l.Append([]byte("new-04"), []byte("new-05"))
		require.NoError(t, err)

		_, data := scanAllData(t, s)
		require.Equal(t, []string{"old-02", "old-03", "new-04", "new-05"}, data)
	})

	t.Run("a scanner at the cut sees what is appended after it", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for i := range 6 {
			_, err = l.Append([]byte(fmt.Sprintf("old-%02d", i)))
			require.NoError(t, err)
		}

		s, err := NewScanner(l)
		require.NoError(t, err)

		for range 4 {
			require.True(t, s.Scan())
		}

		require.NoError(t, l.TruncateAfter(3))

		_, err = l.Append([]byte("new-04"))
		require.NoError(t, err)

		_, data := scanAllData(t, s)
		require.Equal(t, []string{"new-04"}, data)
	})

	t.Run("reports ErrSegmentReaped after segment close", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)

		_, err = l.Append([]byte("a"), []byte("b"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)
		require.True(t, s.Scan())

		require.NoError(t, l.Close())

		require.False(t, s.Scan())
		require.ErrorIs(t, s.Err(), ErrSegmentReaped)
	})
}

func TestScanner_ScanCheckpoints(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions()
	opts.Storage.CheckpointStride = 8

	l, err := New(t.TempDir(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	const total = 20

	for i := range total {
		_, err = l.Append([]byte(strconv.Itoa(i)))
		require.NoError(t, err)
	}

	s, err := NewScanner(l)
	require.NoError(t, err)

	var got []string
	for s.Scan() {
		require.Equal(t, uint64(len(got)), s.LastSequence())
		got = append(got, string(s.Data()))
	}

	require.NoError(t, s.Err())
	require.Len(t, got, total, "checkpoint frames must not be returned as records")
	require.Equal(t, "0", got[0])
	require.Equal(t, "19", got[total-1])
}

func scanAllData(t *testing.T, s *Scanner) (seqs []uint64, data []string) {
	t.Helper()

	for s.Scan() {
		seqs = append(seqs, s.LastSequence())
		data = append(data, string(s.Data()))
	}

	require.NoError(t, s.Err())

	return seqs, data
}

// corruptByteAt flips the byte at the given file offset.
func corruptByteAt(t *testing.T, path string, offset int64) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })

	b := []byte{0}
	_, err = f.ReadAt(b, offset)
	require.NoError(t, err)

	b[0] ^= 0xFF
	_, err = f.WriteAt(b, offset)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
}

func TestNewScanner(t *testing.T) {
	t.Parallel()

	t.Run("reads records larger than 1 MiB completely", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxPayloadSize = 4 << 20

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		big := bytes.Repeat([]byte("0123456789abcdef"), 3<<16) // 3 MiB
		_, err = l.Append([]byte("small"), big, []byte("after"))
		require.NoError(t, err)

		s, err := NewScanner(l)
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Equal(t, []uint64{0, 1, 2}, seqs)
		require.Equal(t, "small", data[0])
		require.Equal(t, string(big), data[1])
		require.Equal(t, "after", data[2])
	})
}

func TestNewLimitedScanner(t *testing.T) {
	t.Parallel()

	newLogWith := func(t *testing.T, opts Options, payloads ...string) (*Log, string) {
		t.Helper()

		dir := t.TempDir()
		l, err := New(dir, opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		for _, p := range payloads {
			_, err = l.Append([]byte(p))
			require.NoError(t, err)
		}

		return l, dir
	}

	t.Run("cuts every payload off at the limit", func(t *testing.T) {
		t.Parallel()

		l, _ := newLogWith(t, DefaultOptions(), "alpha", "be", "gamma")

		s, err := NewLimitedScanner(l, 3)
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Equal(t, []uint64{0, 1, 2}, seqs)
		require.Equal(t, []string{"alp", "be", "gam"}, data)
	})

	t.Run("a limit of 0 reads the headers only", func(t *testing.T) {
		t.Parallel()

		l, _ := newLogWith(t, DefaultOptions(), "alpha", "be", "gamma")

		s, err := NewLimitedScanner(l, 0)
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Equal(t, []uint64{0, 1, 2}, seqs)
		require.Equal(t, []string{"", "", ""}, data)
	})

	t.Run("does not verify the payload, a full scanner does", func(t *testing.T) {
		t.Parallel()

		l, dir := newLogWith(t, DefaultOptions(), "abcdefgh")

		// the last byte of the payload, behind the 72 byte segment header and the 16 byte frame header
		corruptByteAt(t, onlySegmentPath(t, dir), 72+16+7)

		limited, err := NewLimitedScanner(l, 4)
		require.NoError(t, err)

		_, data := scanAllData(t, limited)
		require.Equal(t, []string{"abcd"}, data)

		full, err := NewScanner(l)
		require.NoError(t, err)
		require.False(t, full.Scan())
		require.ErrorIs(t, full.Err(), frame.ErrInvalidChecksum)
	})

	t.Run("reads across segments and seeks", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512

		payloads := make([]string, 0, 40)
		for i := range 40 {
			payloads = append(payloads, "record-"+strconv.Itoa(i))
		}

		l, _ := newLogWith(t, opts, payloads...)

		s, err := NewLimitedScanner(l, 8)
		require.NoError(t, err)

		seqs, data := scanAllData(t, s)
		require.Len(t, seqs, 40)
		require.Equal(t, "record-0", data[0])
		require.Equal(t, "record-3", data[39][:8])

		s, err = NewLimitedScanner(l, 8)
		require.NoError(t, err)
		require.NoError(t, s.SeekSequence(30))
		require.True(t, s.Scan())
		require.Equal(t, uint64(30), s.LastSequence())
		require.Equal(t, "record-3", string(s.Data()))
	})

	t.Run("keeps seeing appends after it reached the end", func(t *testing.T) {
		t.Parallel()

		l, _ := newLogWith(t, DefaultOptions(), "a", "b")

		s, err := NewLimitedScanner(l, 1)
		require.NoError(t, err)
		require.True(t, s.Scan())
		require.True(t, s.Scan())
		require.False(t, s.Scan())
		require.NoError(t, s.Err())
	})
}

func TestScanner_ScanDuringTruncation(t *testing.T) {
	t.Parallel()

	l, err := New(t.TempDir(), DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	var (
		wg    sync.WaitGroup
		stop  atomic.Bool
		errCh = make(chan error, 4)
	)

	for range 3 {
		wg.Go(func() {
			for !stop.Load() {
				s, scanErr := NewScanner(l)
				if scanErr != nil {
					errCh <- scanErr
					return
				}

				for s.Scan() {
					want := fmt.Sprintf("r%d-", s.LastSequence())
					if !strings.HasPrefix(string(s.Data()), want) {
						errCh <- fmt.Errorf("record %d has payload %q", s.LastSequence(), s.Data())
						return
					}
				}

				if scanErr = s.Err(); scanErr != nil && !errors.Is(scanErr, ErrSegmentReaped) {
					errCh <- scanErr
					return
				}
			}
		})
	}

	// the log is only written to from this goroutine
	next := uint64(0)
	for round := range 300 {
		for range 5 {
			_, err = l.Append([]byte(fmt.Sprintf("r%d-%d", next, round)))
			require.NoError(t, err)

			next++
		}

		if round%3 == 0 {
			next -= 4
			require.NoError(t, l.TruncateAfter(next-1))
		}
	}

	stop.Store(true)
	wg.Wait()
	close(errCh)

	for err = range errCh {
		require.NoError(t, err)
	}
}

func countSegments(t *testing.T, l *Log) int {
	t.Helper()

	entries, err := os.ReadDir(l.Path())
	require.NoError(t, err)

	n := 0

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".bin" {
			n++
		}
	}

	return n
}
