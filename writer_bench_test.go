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
	"fmt"
	"sync"
	"testing"
	"time"

	"stuw.net/alog/internal/testutil"
)

func BenchmarkStart(b *testing.B) {
	for _, p := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("parallelism=%d", p), func(b *testing.B) {
			opts := DefaultOptions()
			opts.Storage.MaxSegments = 4
			opts.Storage.MaxSegmentSize = 64 * 1024 * 1024
			l := GetWritableBenchLog(b, BenchLogMeta{Options: opts})
			ctx, cancel := testutil.ContextWithCancel(b)
			b.Cleanup(cancel)

			w := NewWriter(
				l,
				WithWriterMaxLength(1024),
				WithWriterMaxSize(4<<20),
				WithWriterMaxDelay(100*time.Microsecond),
			)
			w.Start(ctx)

			payload := testutil.BenchmarkPayload(16)
			b.SetBytes(int64(len(payload)))
			b.SetParallelism(p)
			var (
				errMu    sync.Mutex
				firstErr error
			)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := w.Append(ctx, payload); err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						errMu.Unlock()
						return
					}
				}
			})
			if firstErr != nil {
				b.Fatalf("Writer.Append() error = %v", firstErr)
			}
			testutil.ReportDomainMetrics(b, 1, int64(len(payload)))
		})
	}

	b.Run("multipart/parallelism=4", func(b *testing.B) {
		opts := DefaultOptions()
		opts.Storage.MaxSegments = 4
		opts.Storage.MaxSegmentSize = 64 * 1024 * 1024
		l := GetWritableBenchLog(b, BenchLogMeta{Options: opts})
		ctx, cancel := testutil.ContextWithCancel(b)
		b.Cleanup(cancel)

		w := NewWriter(
			l,
			WithWriterMaxLength(1024),
			WithWriterMaxSize(4<<20),
			WithWriterMaxDelay(100*time.Microsecond),
		)
		w.Start(ctx)

		payload := testutil.BenchmarkPayload(16)
		record := MultipartPayload{payload[:8], payload[8:]}
		b.SetBytes(int64(len(payload)))
		b.SetParallelism(4)
		var (
			errMu    sync.Mutex
			firstErr error
		)
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := w.AppendMultipart(ctx, record); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
			}
		})
		if firstErr != nil {
			b.Fatalf("Writer.AppendMultipart() error = %v", firstErr)
		}
		testutil.ReportDomainMetrics(b, 1, int64(len(payload)))
	})
}
