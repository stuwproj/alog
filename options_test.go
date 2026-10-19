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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOptions(t *testing.T) {
	t.Parallel()

	withSegmentSize := func(size int64) StorageOptions {
		opts := DefaultOptions().Storage
		opts.MaxSegmentSize = size
		opts.Preallocate = -1

		return opts
	}

	tests := []struct {
		name    string
		storage StorageOptions
		want    int64
	}{
		{"default segment size is 1/64 of it", DefaultOptions().Storage, 2 << 20},
		{"scales with the segment size", withSegmentSize(1 << 30), 16 << 20},
		{"has a floor", withSegmentSize(1 << 20), 64 << 10},
		{"never exceeds an empty segment", withSegmentSize(4096), 4096 - 72 - 32},
		{"is 1 MiB without a segment size limit", withSegmentSize(0), 1 << 20},
		{"is unlimited when nothing fits at all", withSegmentSize(100), -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := normalizeOptions(Options{Storage: tc.storage})
			require.Equal(t, tc.want, got.Storage.MaxPayloadSize)
		})
	}

	t.Run("selects the default checkpoint stride for 0", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.CheckpointStride = 0
		require.Equal(t, uint16(4096), normalizeOptions(opts).Storage.CheckpointStride)

		opts.Storage.CheckpointStride = 8
		require.Equal(t, uint16(8), normalizeOptions(opts).Storage.CheckpointStride)
	})

	t.Run("preallocates a segment by default", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name           string
			maxSegmentSize int64
			preallocate    int64
			want           int64
		}{
			{"the segment size", 4 << 20, 0, 4 << 20},
			{"128 MiB when the segment size is not limited", 0, 0, 128 << 20},
			{"128 MiB when the segment size is negative", -1, 0, 128 << 20},
			{"an explicit size", 4 << 20, 1 << 20, 1 << 20},
			{"nothing when disabled", 4 << 20, -1, -1},
			{"an explicit size without a segment size limit", 0, 1 << 20, 1 << 20},
		} {
			opts := DefaultOptions()
			opts.Storage.MaxSegmentSize = tc.maxSegmentSize
			opts.Storage.Preallocate = tc.preallocate

			require.Equal(t, tc.want, normalizeOptions(opts).Storage.Preallocate, tc.name)
		}
	})

	t.Run("keeps an explicit value", func(t *testing.T) {
		t.Parallel()

		opts := DefaultOptions()
		opts.Storage.MaxPayloadSize = 123
		require.Equal(t, int64(123), normalizeOptions(opts).Storage.MaxPayloadSize)

		opts.Storage.MaxPayloadSize = -5
		require.Equal(t, int64(-5), normalizeOptions(opts).Storage.MaxPayloadSize)
	})
}

func newLogWithMaxPayload(t *testing.T, limit int64) *Log {
	t.Helper()

	opts := DefaultOptions()
	opts.Storage.MaxPayloadSize = limit

	l, err := New(t.TempDir(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	return l
}

func TestLog_Append(t *testing.T) {
	t.Parallel()

	t.Run("accepts a payload of exactly the maximum size", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 100)

		_, err := l.Append(bytes.Repeat([]byte("x"), 100))
		require.NoError(t, err)
		require.Equal(t, uint64(1), l.NextSequence())
	})

	t.Run("rejects a larger payload before writing anything", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 100)
		before := l.DiskSize()

		_, err := l.Append(bytes.Repeat([]byte("x"), 101))
		require.ErrorIs(t, err, ErrPayloadTooLarge)
		require.Equal(t, uint64(0), l.NextSequence())
		require.Equal(t, before, l.DiskSize())

		_, err = l.AppendOne(bytes.Repeat([]byte("x"), 101))
		require.ErrorIs(t, err, ErrPayloadTooLarge)
	})

	t.Run("rejects the whole batch when 1 payload is too large", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 100)

		_, err := l.Append([]byte("fine"), bytes.Repeat([]byte("x"), 101), []byte("also fine"))
		require.ErrorIs(t, err, ErrPayloadTooLarge)
		require.Equal(t, uint64(0), l.NextSequence(), "nothing of the batch may be written")

		_, err = l.Append([]byte("fine"), []byte("also fine"))
		require.NoError(t, err)
		require.Equal(t, uint64(2), l.NextSequence())
	})

	t.Run("counts all parts of a multipart payload", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 100)

		parts := MultipartPayload{bytes.Repeat([]byte("a"), 60), bytes.Repeat([]byte("b"), 41)}
		_, err := l.AppendMultipart(parts)
		require.ErrorIs(t, err, ErrPayloadTooLarge)

		parts = MultipartPayload{bytes.Repeat([]byte("a"), 60), bytes.Repeat([]byte("b"), 40)}
		_, err = l.AppendMultipart(parts)
		require.NoError(t, err)
	})

	t.Run("does not keep the payloads of the caller", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 0)

		_, err := l.Append([]byte("a"), []byte("b"), []byte("c"))
		require.NoError(t, err)

		for i := range 3 {
			require.Nil(t, l.scratch[i][0], "payload %d is still referenced", i)
		}
	})

	t.Run("a negative maximum removes the limit", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, -1)

		_, err := l.Append(bytes.Repeat([]byte("x"), 3<<20))
		require.NoError(t, err)
	})

	t.Run("the default maximum applies", func(t *testing.T) {
		t.Parallel()

		l, err := New(t.TempDir(), DefaultOptions())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		_, err = l.Append(bytes.Repeat([]byte("x"), 2<<20))
		require.NoError(t, err)

		_, err = l.Append(bytes.Repeat([]byte("x"), 2<<20+1))
		require.ErrorIs(t, err, ErrPayloadTooLarge)
	})
}

func TestWriter_AppendMultipart(t *testing.T) {
	t.Parallel()

	t.Run("does not modify the parts of the caller", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 0)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(3), WithWriterMaxDelay(time.Minute))
		w.Start(ctx)

		parts := MultipartPayload{[]byte("head-"), []byte("tail")}
		sent := func(payloads ...func() error) {
			var wg sync.WaitGroup
			for _, send := range payloads {
				wg.Go(func() { require.NoError(t, send()) })
			}

			wg.Wait()
		}

		multipart := func() error { _, err := w.AppendMultipart(ctx, parts); return err }
		plain := func() error { _, err := w.Append(ctx, []byte("plain")); return err }

		// the multipart request lands in some slot of the batch, and the next batches
		// have plain requests in every slot
		sent(multipart, plain, plain)
		sent(plain, plain, plain)
		sent(plain, plain, plain)

		require.Equal(t, MultipartPayload{[]byte("head-"), []byte("tail")}, parts)
		require.Equal(t, uint64(9), l.NextSequence())
	})

	t.Run("an oversized request does not fail the requests it would share a batch with", func(t *testing.T) {
		t.Parallel()

		l := newLogWithMaxPayload(t, 100)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		t.Cleanup(cancel)

		w := NewWriter(l, WithWriterMaxLength(16), WithWriterMaxDelay(50*time.Millisecond))
		w.Start(ctx)

		errs := make([]error, 4)

		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() {
				payload := []byte("fine")
				if i == 2 {
					payload = bytes.Repeat([]byte("x"), 101)
				}

				_, errs[i] = w.Append(ctx, payload)
			})
		}

		wg.Wait()

		for i, err := range errs {
			if i == 2 {
				require.ErrorIs(t, err, ErrPayloadTooLarge)
				continue
			}

			require.NoError(t, err, "request %d", i)
		}

		_, err := w.AppendMultipart(ctx, MultipartPayload{bytes.Repeat([]byte("a"), 60), bytes.Repeat([]byte("b"), 41)})
		require.ErrorIs(t, err, ErrPayloadTooLarge)
		require.Equal(t, uint64(3), l.NextSequence())
	})
}
