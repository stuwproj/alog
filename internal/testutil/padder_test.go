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

package testutil

import (
	"flag"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	padderUntimed = 2 * time.Millisecond
	padderOp      = 200 * time.Microsecond
)

func spin(d time.Duration) {
	start := time.Now()
	for time.Since(start) < d {
		runtime.Gosched()
	}
}

func setBenchtime(t *testing.T, value string) {
	t.Helper()

	f := flag.Lookup("test.benchtime")
	require.NotNil(t, f)

	prev := f.Value.String()
	require.NoError(t, flag.Set("test.benchtime", value))
	t.Cleanup(func() { require.NoError(t, flag.Set("test.benchtime", prev)) })
}

// benchPadded runs a benchmark with expensive untimed work around a cheap
// operation. It returns the result, the total padding, and the number of
// iterations in which Resume itself waited instead of the next Pause.
func benchPadded() (res testing.BenchmarkResult, totalPadding time.Duration, waitedInResume int) {
	res = testing.Benchmark(func(b *testing.B) {
		p := NewPadder(b)
		for b.Loop() {
			p.Pause()
			time.Sleep(padderUntimed)

			before := p.padded
			p.Resume()

			if p.padded != before {
				waitedInResume++
			}

			spin(padderOp)
		}

		p.Report()
		totalPadding = p.padded
	})

	return res, totalPadding, waitedInResume
}

// These tests measure time and change global test flags, so they don't run in parallel.
//
//nolint:paralleltest
func TestPadder(t *testing.T) {
	t.Run("limits iterations and keeps ns/op honest", func(t *testing.T) {
		setBenchtime(t, "100ms")

		padded, totalPadding, _ := benchPadded()

		unpadded := testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				time.Sleep(padderUntimed)
				b.StartTimer()

				spin(padderOp)
			}
		})

		require.NotZero(t, padded.N)
		require.NotZero(t, unpadded.N)

		// untimed work is about 10x the operation, so padding must cut the count a lot
		require.Less(t, padded.N*3, unpadded.N)

		// every pad is at least as long as the untimed work it mirrors,
		// except for the one owed after the last iteration
		require.GreaterOrEqual(t, totalPadding, time.Duration(padded.N-1)*padderUntimed)

		// the timed duration includes the padding ...
		require.Greater(t, padded.T/time.Duration(padded.N), padderUntimed)

		// ... but the reported ns/op does not
		require.GreaterOrEqual(t, padded.NsPerOp(), padderOp.Nanoseconds())
		require.Less(t, padded.NsPerOp(), 3*padderOp.Nanoseconds())
	})

	t.Run("waits after the operation, not in Resume", func(t *testing.T) {
		setBenchtime(t, "50ms")

		res, totalPadding, waitedInResume := benchPadded()

		require.Greater(t, res.N, 1)
		require.Positive(t, totalPadding)
		require.Zero(t, waitedInResume)
	})

	t.Run("does not pad a fixed iteration count", func(t *testing.T) {
		setBenchtime(t, "5x")

		res, totalPadding, _ := benchPadded()

		require.Equal(t, 5, res.N)
		require.Zero(t, totalPadding)
	})
}
