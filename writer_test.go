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
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriter_Start(t *testing.T) {
	t.Parallel()

	t.Run("append fails after stop", func(t *testing.T) {
		l := newLoadedLog(t, 2, 1024)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)
		cancel()

		require.Eventually(t, func() bool {
			appendCtx, appendCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer appendCancel()
			_, err := w.Append(appendCtx, []byte("x"))
			return errors.Is(err, ErrStopped)
		}, time.Second, 10*time.Millisecond)
	})
}

func TestWriter_Append(t *testing.T) {
	t.Parallel()

	t.Run("assigns sequence numbers", func(t *testing.T) {
		l := newLoadedLog(t, 2, 1024)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)

		seq0, err := w.Append(ctx, []byte("a"))
		require.NoError(t, err)
		require.Equal(t, uint64(0), seq0)

		seq1, err := w.Append(ctx, []byte("b"))
		require.NoError(t, err)
		require.Equal(t, uint64(1), seq1)
	})

	t.Run("append multipart assigns sequence numbers", func(t *testing.T) {
		l := newLoadedLog(t, 2, 1024)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)

		seq0, err := w.AppendMultipart(ctx, MultipartPayload{[]byte("h:"), []byte("a")})
		require.NoError(t, err)
		require.Equal(t, uint64(0), seq0)

		seq1, err := w.AppendMultipart(ctx, MultipartPayload{[]byte("h:"), []byte("b")})
		require.NoError(t, err)
		require.Equal(t, uint64(1), seq1)
	})

	t.Run("with canceled context", func(t *testing.T) {
		l := newLoadedLog(t, 2, 1024)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)

		canceledCtx, canceled := context.WithCancel(t.Context())
		canceled()

		_, err := w.Append(canceledCtx, []byte("x"))
		require.ErrorIs(t, err, context.Canceled)

		cancel()
	})

	t.Run("propagates segment full", func(t *testing.T) {
		l := newLoadedLog(t, 1, 64)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)

		_, err := w.Append(ctx, make([]byte, 256))
		require.ErrorIs(t, err, ErrSegmentFull)
	})

	t.Run("with sync on append propagates sync failure", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Storage.SyncOnAppend = true
		opts.Storage.MaxSegmentSize = 1024
		opts.Storage.MaxSegments = 2

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
		w.Start(ctx)

		_, err = w.Append(ctx, []byte("payload"))
		require.NoError(t, err)
	})

	t.Run("a batch that is too large for the log is written in parts", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 256
		opts.Storage.MaxPayloadSize = -1

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(4), WithWriterMaxDelay(time.Minute))
		w.Start(ctx)

		// 1 record fits in an empty segment, 4 do not
		payload := bytes.Repeat([]byte("x"), 40)
		seqs := make([]uint64, 4)
		errs := make([]error, 4)

		var wg sync.WaitGroup
		for i := range seqs {
			wg.Go(func() { seqs[i], errs[i] = w.Append(ctx, payload) })
		}

		wg.Wait()

		for i, err := range errs {
			require.NoError(t, err, "request %d", i)
		}

		slices.Sort(seqs)
		require.Equal(t, []uint64{0, 1, 2, 3}, seqs)
		require.Equal(t, uint64(4), l.NextSequence())
	})

	t.Run("a request that cannot fit in a segment fails alone", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxSegmentSize = 256
		opts.Storage.MaxPayloadSize = -1

		l, err := New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(4), WithWriterMaxDelay(time.Minute))
		w.Start(ctx)

		errs := make([]error, 4)

		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() {
				payload := []byte("small")
				if i == 1 {
					payload = bytes.Repeat([]byte("x"), 400)
				}

				_, errs[i] = w.Append(ctx, payload)
			})
		}

		wg.Wait()

		for i, err := range errs {
			if i == 1 {
				require.ErrorIs(t, err, ErrBatchTooLarge)
				continue
			}

			require.NoError(t, err, "request %d", i)
		}

		require.Equal(t, uint64(3), l.NextSequence())
	})

	t.Run("more requests than fit in a single write", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(5000), WithWriterMaxDelay(100*time.Millisecond))
		w.Start(ctx)

		const total = 1500

		errs := make([]error, total)

		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() { _, errs[i] = w.Append(ctx, []byte("record")) })
		}

		wg.Wait()

		for i, err := range errs {
			require.NoError(t, err, "request %d", i)
		}

		require.Equal(t, uint64(total), l.NextSequence())
	})
}

func TestWriter_StartHelperPattern(t *testing.T) {
	t.Parallel()

	l := newLoadedLog(t, 2, 1024)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	ctx, cancel := context.WithCancel(t.Context())
	w := NewWriter(l, WithWriterMaxLength(1), WithWriterMaxSize(1), WithWriterMaxDelay(time.Hour))
	w.Start(ctx)

	seq0, err := w.Append(ctx, []byte("a"))
	require.NoError(t, err)
	seq1, err := w.Append(ctx, []byte("b"))
	require.NoError(t, err)
	require.Equal(t, uint64(0), seq0)
	require.Equal(t, uint64(1), seq1)

	cancel()

	require.Eventually(t, func() bool {
		appendCtx, appendCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer appendCancel()
		_, appendErr := w.Append(appendCtx, []byte("c"))
		return errors.Is(appendErr, ErrStopped)
	}, time.Second, 10*time.Millisecond)
}

func TestWriter_ConcurrentAppends(t *testing.T) {
	t.Parallel()

	l := newLoadedLog(t, 4, 1<<20)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	w := NewWriter(l, WithWriterMaxLength(16), WithWriterMaxSize(1<<20), WithWriterMaxDelay(time.Millisecond))
	w.Start(ctx)

	const n = 64
	seqs := make(chan uint64, n)
	errs := make(chan error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seq, err := w.Append(ctx, []byte{byte(i)})
			if err != nil {
				errs <- err
				return
			}
			seqs <- seq
		}(i)
	}

	wg.Wait()
	close(seqs)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	seen := map[uint64]struct{}{}
	for seq := range seqs {
		_, exists := seen[seq]
		require.False(t, exists)
		seen[seq] = struct{}{}
	}
	require.Len(t, seen, n)
}

func newLoadedLog(t *testing.T, maxSegments int, maxSegmentSize int64) *Log {
	t.Helper()

	l, err := New(t.TempDir(), Options{
		Storage: StorageOptions{
			MaxDiskSize:    0,
			MaxSegmentSize: maxSegmentSize,
			MaxSegments:    maxSegments,
		},
	})
	require.NoError(t, err)

	return l
}
