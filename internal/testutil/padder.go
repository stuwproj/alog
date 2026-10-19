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
	"strings"
	"testing"
	"time"
)

// Padder keeps expensive untimed per-iteration work (fixture setup, teardown)
// from stretching the wall time of a benchmark.
//
// b.Loop chooses the iteration count so that the timed part adds up to
// benchtime. When every iteration does a lot of untimed work around a cheap
// operation, that count becomes huge and the benchmark runs for minutes.
// Padder busy-waits inside the timed region for as long as the untimed work
// took, so the iteration count is chosen based on the real cost of an
// iteration. The padding is removed again from the reported ns/op.
//
// The wait happens after the timed operation, at the next Pause, so the
// operation runs directly after the untimed work, as in an unpadded benchmark.
// The padding owed after the last iteration is not waited for.
//
// Padding busy-waits instead of sleeping on purpose: a sleeping goroutine lets
// the CPU idle, which made the operations that follow measurably slower (about
// twice as slow for operations of around 15 microseconds).
//
// Use it inside a b.Loop loop, in place of b.StopTimer and b.StartTimer:
//
//	p := testutil.NewPadder(b)
//	for b.Loop() {
//		p.Pause()
//		fixture := newFixture(b)
//		p.Resume()
//
//		fixture.Operation()
//	}
//	p.Report()
//
// Untimed work stays under a stopped timer, so B/op and allocs/op are not
// affected. Metrics derived from b.Elapsed, such as MB/s, include the padding
// and should use Elapsed instead.
//
// With a fixed iteration count (-benchtime=Nx) the padding has no purpose and
// is skipped.
type Padder struct {
	b        *testing.B
	pausedAt time.Time
	owed     time.Duration
	padded   time.Duration
	paused   bool
	disabled bool
}

// NewPadder creates a Padder for b.
func NewPadder(b *testing.B) *Padder {
	b.Helper()

	return &Padder{b: b, disabled: fixedIterations()}
}

// Pause waits for the padding owed by the previous Resume, then stops the
// benchmark timer and starts measuring untimed work.
// Calling Pause while already paused has no effect.
func (p *Padder) Pause() {
	p.b.Helper()

	if p.paused {
		return
	}

	p.padOwed()
	p.b.StopTimer()
	p.paused = true
	p.pausedAt = time.Now()
}

// Resume restarts the benchmark timer and records how long the untimed work
// since Pause took. The matching wait happens at the next Pause.
// The timer must be running again before the next b.Loop call.
func (p *Padder) Resume() {
	p.b.Helper()

	if !p.paused {
		p.b.Fatal("testutil.Padder: Resume called without Pause")
	}

	untimed := time.Since(p.pausedAt)
	p.paused = false
	p.b.StartTimer()

	if !p.disabled {
		p.owed += untimed
	}
}

// padOwed busy-waits inside the timed region for the owed padding.
func (p *Padder) padOwed() {
	if p.owed <= 0 {
		return
	}

	start := time.Now()

	for time.Since(start) < p.owed { //nolint:revive // busy-wait on purpose
		// yielding (runtime.Gosched) can move the goroutine to another
		// core, so the next operation would run with cold caches.
	}

	p.padded += time.Since(start)
	p.owed = 0
}

// Elapsed returns the measured benchmark duration without the padding.
func (p *Padder) Elapsed() time.Duration {
	return p.b.Elapsed() - p.padded
}

// Report replaces the reported ns/op with one that excludes the padding.
func (p *Padder) Report() {
	p.b.Helper()

	if p.b.N <= 0 {
		return
	}

	p.b.ReportMetric(float64(p.Elapsed())/float64(p.b.N), "ns/op")
}

// fixedIterations reports whether the benchmark runs a fixed number of
// iterations (-benchtime=Nx).
func fixedIterations() bool {
	f := flag.Lookup("test.benchtime")

	return f != nil && strings.HasSuffix(f.Value.String(), "x")
}
