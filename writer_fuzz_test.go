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

package alog_test

import (
	"context"
	"testing"
	"time"

	"stuw.net/alog"
	"stuw.net/alog/internal/testutil"
	"github.com/stretchr/testify/require"
)

const (
	maxCallers   = 8
	maxBufferLen = 32
	payloadCap   = 256
)

type appendResult struct {
	err error
	seq uint64
}

// FuzzLog_ConcurrentAppendBounds exercises concurrent append calls with mixed canceled
// contexts and checks sequence uniqueness.
func FuzzLog_ConcurrentAppendBounds(f *testing.F) {
	f.Add(uint8(4), uint16(4), []byte{1, 2, 3, 4}, byte(0))

	f.Fuzz(func(t *testing.T, callers uint8, maxLen uint16, payload []byte, cancelMask byte) {
		// normalize fuzz dimensions into bounded append inputs
		payload = createPayload(callers, maxLen, cancelMask, payload)
		nCallers := callerCount(callers)
		bufLen := bufferLen(maxLen)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		opts := alog.DefaultOptions()
		opts.Storage.MaxDiskSize = 0
		opts.Storage.MaxSegmentSize = 1 << 20
		opts.Storage.MaxSegments = 3

		l, err := alog.New(t.TempDir(), opts)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, l.Close()) })

		w := alog.NewWriter(
			l,
			alog.WithWriterMaxLength(bufLen),
			alog.WithWriterMaxSize(1<<12),
			alog.WithWriterMaxDelay(2*time.Nanosecond),
		)
		w.Start(ctx)

		results := make(chan appendResult, nCallers)

		// each caller does one append; successful appends must produce unique seqs
		for i := range nCallers {
			callCtx := callerContext(ctx, cancelMask, i)
			seq, appendErr := w.Append(callCtx, payload)
			results <- appendResult{seq: seq, err: appendErr}
		}
		close(results)

		// verify successful append sequences contain no duplicates
		requireUniqueSuccesses(t, results)
	})
}

func createPayload(callers uint8, maxLen uint16, cancelMask byte, payload []byte) []byte {
	if len(payload) == 0 {
		payload = []byte{1}
	}

	return testutil.BoundedFuzzBytes(payload, payloadCap, uint32(callers)^uint32(maxLen)^uint32(cancelMask))
}

func callerCount(callers uint8) int {
	return int(callers%maxCallers) + 1
}

func bufferLen(maxLen uint16) int {
	return int(maxLen%maxBufferLen) + 1
}

func callerContext(ctx context.Context, cancelMask byte, i int) context.Context {
	if cancelMask&(1<<uint(i%8)) == 0 {
		return ctx
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()

	return cctx
}

func requireUniqueSuccesses(t *testing.T, results <-chan appendResult) {
	t.Helper()
	success := map[uint64]struct{}{}
	for r := range results {
		if r.err != nil {
			continue
		}
		if _, dup := success[r.seq]; dup {
			t.Fatalf("duplicate sequence %d", r.seq)
		}
		success[r.seq] = struct{}{}
	}
}
