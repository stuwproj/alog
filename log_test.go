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
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

// scanUntilDone repeatedly creates scanners and scans the log while appends
// are in progress. Only scanner creation and scanner methods are used
// concurrently with the writer; Log accessors are not concurrency-safe.
// Scanned sequences must be strictly contiguous within each pass.
func scanUntilDone(l *Log, done <-chan struct{}, errCh chan<- error) {
	for {
		s, err := NewScanner(l)
		if err != nil {
			errCh <- err
			return
		}

		first := true
		var prev uint64
		for s.Scan() {
			seq := s.LastSequence()
			if !first && seq != prev+1 {
				errCh <- fmt.Errorf("non-contiguous scan: %d after %d", seq, prev)
				return
			}

			first = false
			prev = seq
		}

		if err = s.Err(); err != nil {
			errCh <- err
			return
		}

		_ = s.Close()

		select {
		case <-done:
			return
		default:
		}
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions()
	require.Equal(t, int64(10*1024*1024*1024), opts.Storage.MaxDiskSize)
	require.Equal(t, 0, opts.Storage.MaxSegments)
	require.Equal(t, int64(128*1024*1024), opts.Storage.MaxSegmentSize)
	require.False(t, opts.Storage.SyncOnAppend)
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("accepts unbounded segments", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxDiskSize = 0
		opts.Storage.MaxSegmentSize = 0

		log, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		require.NoError(t, log.Close())
	})

	t.Run("applies defaults for zero options", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), Options{})
		require.NoError(t, err)
		require.NotNil(t, l)
		require.NoError(t, l.Close())
	})

	t.Run("rejects a max payload size that does not fit in an empty segment", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 4096
		opts.Storage.Preallocate = -1

		opts.Storage.MaxPayloadSize = 4096 - 72 - 32 + 1
		_, err := New(t.TempDir(), opts)
		require.Error(t, err)

		opts.Storage.MaxPayloadSize = 4096 - 72 - 32
		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)

		_, err = l.Append(make([]byte, 4096-72-32))
		require.NoError(t, err, "a payload of the maximum size has to fit in an empty segment")
		require.NoError(t, l.Close())
	})

	t.Run("a payload of the maximum size fits when 1 record completes the checkpoint stride", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 512
		opts.Storage.Preallocate = -1
		opts.Storage.CheckpointStride = 1

		// the segment header, the frame header, a checkpoint and the end marker
		capacity := int64(512 - 72 - 3*16)

		opts.Storage.MaxPayloadSize = capacity + 1
		_, err := New(t.TempDir(), opts)
		require.Error(t, err, "the checkpoint has to fit as well")

		opts.Storage.MaxPayloadSize = capacity
		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)

		_, err = l.Append(make([]byte, capacity))
		require.NoError(t, err)
		require.NoError(t, l.Close())
	})

	t.Run("a directory belongs to 1 log at a time", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		first, err := New(dir, DefaultOptions())
		require.NoError(t, err)

		_, err = New(dir, DefaultOptions())
		require.ErrorIs(t, err, ErrLocked)

		// closing releases it, also when it is done more than once
		require.NoError(t, first.Close())
		require.NoError(t, first.Close())

		second, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		require.NoError(t, second.Close())
	})

	t.Run("a read-only log does not take the lock, and is not stopped by it", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		writer, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, writer.Close()) })

		_, err = writer.Append([]byte("a"))
		require.NoError(t, err)

		ro := DefaultOptions()
		ro.Storage.ReadOnly = true

		reader, err := New(dir, ro)
		require.NoError(t, err)
		require.NoError(t, reader.Close())

		_, err = New(dir, DefaultOptions())
		require.ErrorIs(t, err, ErrLocked, "the reader must not have taken over the lock")
	})

	t.Run("a log that fails to load releases the lock", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		l, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		_, err = l.Append([]byte("a"))
		require.NoError(t, err)
		require.NoError(t, l.Close())

		small := DefaultOptions()
		small.Storage.MaxDiskSize = 50 // less than a segment with a record
		small.Storage.Preallocate = -1

		_, err = New(dir, small)
		require.ErrorIs(t, err, ErrDiskFull)

		l, err = New(dir, DefaultOptions())
		require.NoError(t, err)
		require.NoError(t, l.Close())
	})

	t.Run("the lock file is not a segment", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		l, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		require.NoError(t, l.Close())

		_, err = os.Stat(filepath.Join(dir, lockFileName))
		require.NoError(t, err)

		l, err = New(dir, DefaultOptions())
		require.NoError(t, err)
		require.NoError(t, l.Close())
	})

	t.Run("accepts a preallocation size when the segment size is not limited", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 0
		opts.Storage.MaxSegments = 1
		opts.Storage.Preallocate = 1 << 20

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		require.Equal(t, int64(1<<20), l.DiskSize())
		require.NoError(t, l.Close())
	})

	t.Run("rejects a preallocation size that is larger than the segment size", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 1 << 20
		opts.Storage.Preallocate = 2 << 20

		_, err := New(t.TempDir(), opts)
		require.Error(t, err)
	})

	t.Run("applies default sparse index stride", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), Options{Storage: StorageOptions{MaxSegments: 1}})
		require.NoError(t, err)
		require.NotNil(t, l)
		require.NoError(t, l.Close())
	})
}

func TestLog_Load(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	opts.Storage.MaxSegments = 2
	opts.Storage.MaxSegmentSize = 1024

	log, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })

	lastSeq, err := log.Append([]byte("alpha"), []byte("beta"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), lastSeq)

	s, err := NewScanner(log)
	require.NoError(t, err)

	var got []string
	for s.Scan() {
		got = append(got, string(s.Data())+":"+strconv.FormatUint(s.LastSequence(), 10))
	}

	require.NoError(t, s.Err())
	require.Equal(t, []string{"alpha:0", "beta:1"}, got)
}

func TestLog_ReadOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w := DefaultOptions()
	w.Storage.MaxSegments = 2
	w.Storage.MaxSegmentSize = 1024

	seed, err := New(dir, w)
	require.NoError(t, err)
	_, err = seed.Append([]byte("alpha"), []byte("beta"))
	require.NoError(t, err)
	require.NoError(t, seed.Sync())
	require.NoError(t, seed.Close())

	ro := DefaultOptions()
	ro.Storage.MaxSegments = 2
	ro.Storage.MaxSegmentSize = 1024
	ro.Storage.ReadOnly = true

	l, err := New(dir, ro)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	s, err := NewScanner(l)
	require.NoError(t, err)

	var got []string
	for s.Scan() {
		got = append(got, string(s.Data())+":"+strconv.FormatUint(s.LastSequence(), 10))
	}
	require.NoError(t, s.Err())
	require.Equal(t, []string{"alpha:0", "beta:1"}, got)

	_, err = l.Append([]byte("gamma"))
	require.ErrorIs(t, err, os.ErrPermission)
	require.ErrorIs(t, err, storage.ErrReadOnly)

	err = l.Sync()
	require.ErrorIs(t, err, os.ErrPermission)
	require.ErrorIs(t, err, storage.ErrReadOnly)
}

func TestLog_Sync(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()

	log, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })
	_, err = log.Append([]byte("sync-me"))
	require.NoError(t, err)
	require.NoError(t, log.Sync())
}

func TestLog_AppendAndScan(t *testing.T) {
	t.Parallel()

	l := newLoadedLog(t, 0, 1<<20)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	w := NewWriter(l, WithWriterMaxLength(16), WithWriterMaxSize(1<<16), WithWriterMaxDelay(time.Millisecond))
	w.Start(ctx)

	const total = 512
	appendDone := make(chan struct{})
	errCh := make(chan error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		defer close(appendDone)

		for i := range total {
			if _, appendErr := w.Append(ctx, []byte(strconv.Itoa(i))); appendErr != nil {
				errCh <- appendErr
				return
			}
		}
	})

	wg.Go(func() { scanUntilDone(l, appendDone, errCh) })

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	s, err := NewScanner(l)
	require.NoError(t, err)
	require.NoError(t, s.SeekSequence(0))

	count := 0
	for s.Scan() {
		count++
	}

	require.NoError(t, s.Err())
	require.Equal(t, total, count)
}

func TestLog_AppendAndScanWithRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	l, err := New(dir, Options{Storage: StorageOptions{MaxDiskSize: 0, MaxSegmentSize: 4096, MaxSegments: 0}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	w := NewWriter(l, WithWriterMaxLength(8), WithWriterMaxSize(1<<16), WithWriterMaxDelay(time.Millisecond))
	w.Start(ctx)

	const total = 200
	payload := make([]byte, 256)
	appendDone := make(chan struct{})
	errCh := make(chan error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		defer close(appendDone)

		for range total {
			if _, appendErr := w.Append(ctx, payload); appendErr != nil {
				errCh <- appendErr
				return
			}
		}
	})

	wg.Go(func() { scanUntilDone(l, appendDone, errCh) })

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	require.Equal(t, uint64(total), l.NextSequence())

	s, err := NewScanner(l)
	require.NoError(t, err)
	require.NoError(t, s.SeekSequence(uint64(total-1)))
	require.True(t, s.Scan())
	require.Equal(t, uint64(total-1), s.LastSequence())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	segments := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".bin" {
			segments++
		}
	}

	require.Greater(t, segments, 1)
}

func TestLog_DiskSize(t *testing.T) {
	tests := []struct {
		opts     func() Options
		name     string
		expected int64
	}{
		{
			name:     "default options",
			opts:     DefaultOptions,
			expected: 128 << 20,
		},
		{
			name: "max segment size",
			opts: func() Options {
				o := DefaultOptions()
				o.Storage.MaxSegmentSize = 1 << 20
				return o
			},
			expected: 1 << 20,
		},
		{
			name: "max segment size 0",
			opts: func() Options {
				o := DefaultOptions()
				o.Storage.MaxSegmentSize = 0
				return o
			},
			expected: 128 << 20,
		},
		{
			name: "explicit preallocate size",
			opts: func() Options {
				o := DefaultOptions()
				o.Storage.MaxSegmentSize = 2 << 20
				o.Storage.Preallocate = 1 << 20
				return o
			},
			expected: 1 << 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			opts := tt.opts()

			log, err := New(dir, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, log.Close()) })

			_, err = log.Append([]byte("abc"))
			require.NoError(t, err)

			require.Equal(t, tt.expected, log.DiskSize())
		})
	}
}

func TestLog_AppendErrorShape(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	opts.Storage.MaxSegments = 1
	opts.Storage.MaxSegmentSize = 64

	l, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append(make([]byte, 128))
	require.ErrorIs(t, err, ErrSegmentFull)
	require.ErrorIs(t, err, ErrBatchTooLarge)

	t.Run("a batch that needs more buffers than a write takes", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		batch := make([][]byte, 2000)
		for i := range batch {
			batch[i] = []byte("x")
		}

		_, err = l.Append(batch...)
		require.ErrorIs(t, err, ErrBatchTooLarge)
		require.NotErrorIs(t, err, ErrSegmentFull, "the segment has room, the write does not")
		require.NotErrorIs(t, err, ErrPayloadTooLarge)
		require.Equal(t, uint64(0), l.NextSequence())

		_, err = l.Append([]byte("still works"))
		require.NoError(t, err)
	})

	t.Run("a batch with more records than a segment can index", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		batch := make([]MultipartPayload, 70_000)
		for i := range batch {
			batch[i] = MultipartPayload{}
		}

		_, err = l.AppendMultipartBatch(batch)
		require.ErrorIs(t, err, ErrBatchTooLarge)
		require.NotErrorIs(t, err, ErrPayloadTooLarge)
	})
}

func TestLog_FirstSequence(t *testing.T) {
	t.Parallel()

	t.Run("empty log", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		l, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		first, ok := l.FirstSequence()
		require.False(t, ok)
		require.Equal(t, uint64(0), first)
	})

	t.Run("non-empty log", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		l, err := New(dir, DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append([]byte("a"), []byte("b"))
		require.NoError(t, err)

		first, ok := l.FirstSequence()
		require.True(t, ok)
		require.Equal(t, uint64(0), first)
	})
}

func TestLog_TruncateAfter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	l, err := New(dir, DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append([]byte("a"), []byte("b"), []byte("c"))
	require.NoError(t, err)

	require.NoError(t, l.TruncateAfter(1))

	s, err := NewScanner(l)
	require.NoError(t, err)

	got := make([]string, 0, 2)
	for s.Scan() {
		got = append(got, string(s.Data()))
	}
	require.NoError(t, s.Err())
	require.Equal(t, []string{"a", "b"}, got)
}

func TestLog_LoadTrimsInvalidTailFrame(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	opts.Storage.MaxSegments = 1
	opts.Storage.MaxSegmentSize = 1024

	seed, err := New(dir, opts)
	require.NoError(t, err)
	_, err = seed.Append([]byte("a"), []byte("b"))
	require.NoError(t, err)
	require.NoError(t, seed.Sync())
	require.NoError(t, seed.Close())

	segmentPath := onlySegmentPath(t, dir)
	corruptLastByte(t, segmentPath)

	reloaded, err := New(dir, opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

	last, ok := reloaded.LastSequence()
	require.True(t, ok)
	require.Equal(t, uint64(1), last)
}

func TestLog_LoadReadOnlyFailsOnInvalidTailFrameTrim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := DefaultOptions()
	opts.Storage.MaxSegments = 1
	opts.Storage.MaxSegmentSize = 1024

	seed, err := New(dir, opts)
	require.NoError(t, err)
	_, err = seed.Append([]byte("a"), []byte("b"))
	require.NoError(t, err)
	require.NoError(t, seed.Sync())
	require.NoError(t, seed.Close())

	segmentPath := onlySegmentPath(t, dir)
	corruptLastByte(t, segmentPath)

	ro := opts
	ro.Storage.ReadOnly = true
	reloaded, err := New(dir, ro)
	require.NoError(t, err)
	require.NoError(t, reloaded.Close())
}

func onlySegmentPath(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
			continue
		}

		return filepath.Join(dir, entry.Name())
	}

	t.Fatalf("no segment file found in %s", dir)
	return ""
}

func corruptLastByte(t *testing.T, path string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })

	end, err := f.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	require.Greater(t, end, int64(0))

	pos := end - 1
	b := []byte{0}
	_, err = f.ReadAt(b, pos)
	require.NoError(t, err)

	b[0] ^= 0xFF
	_, err = f.WriteAt(b, pos)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
}
