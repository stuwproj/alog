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
	"sync"
	"sync/atomic"
)

// cutRing is the number of recent truncations a lease remembers. A scanner that
// missed more than this is treated as if it was cut off.
const cutRing = 8

// cut records that the sequences from end on were replaced by a truncation.
type cut struct {
	n   uint64
	end uint64
}

// LeaseToken records which truncations of a lease a scanner has seen. Every
// scanner needs its own, and uses it from 1 goroutine.
type LeaseToken struct {
	seen uint64
}

// Lease coordinates scanner reads of a segment's mapped bytes with
// revocation (reaping, recycling, closing) and with truncation.
//
// Scanners Acquire before touching mapped bytes and Release afterwards.
// Revoke prevents new acquisitions and blocks until all readers have released,
// after which the mapped bytes can safely be unmapped.
//
// Truncate does the same for the duration of a function that overwrites mapped
// bytes, after which the lease is usable again. It records the first sequence
// that was replaced, so a scanner that already read beyond it is detected by
// AcquireAt, while a scanner that is positioned before it carries on.
//
// Acquire and Release are lock-free, except while a truncation or revocation
// is in progress. The mutex is only taken by Revoke, Truncate, and by a Release
// that drops the last reader while one of them waits.
//
// A goroutine holding an acquired lease must not call Revoke or Truncate on it.
type Lease struct {
	cuts      atomic.Pointer[[]cut]
	cond      sync.Cond
	mu        sync.Mutex
	reading   atomic.Int64
	cutCount  atomic.Uint64
	revoked   atomic.Bool
	exclusive atomic.Bool
}

// NewLease creates a lease that is not revoked and has no readers.
func NewLease() *Lease {
	l := &Lease{}
	l.cond.L = &l.mu

	return l
}

// NewToken creates a token for a scanner that starts reading now.
func (l *Lease) NewToken() LeaseToken {
	return LeaseToken{seen: l.cutCount.Load()}
}

// Acquire registers a reader. It returns false if the lease has been revoked,
// in which case the caller must not read and must not call Release.
// It waits while a truncation is in progress.
func (l *Lease) Acquire() bool {
	for {
		if l.revoked.Load() {
			return false
		}

		l.reading.Add(1)

		// re-check: Revoke may have been called between the load and the add.
		if l.revoked.Load() {
			l.Release()
			return false
		}

		if !l.exclusive.Load() {
			return true
		}

		// a truncation started, step aside until it is done
		l.Release()
		l.waitExclusive()
	}
}

// AcquireAt registers a reader that is going to read from sequence next on. It
// returns false if the lease has been revoked, or when a truncation replaced
// sequences that the scanner with this token already read.
// In that case the caller must not read and must not call Release.
func (l *Lease) AcquireAt(t *LeaseToken, next uint64) bool {
	if !l.Acquire() {
		return false
	}

	if !l.valid(t, next) {
		l.Release()
		return false
	}

	return true
}

// valid checks the truncations since the token was last updated, and updates it.
func (l *Lease) valid(t *LeaseToken, next uint64) bool {
	count := l.cutCount.Load()
	if t.seen == count {
		return true
	}

	cuts := l.cuts.Load()
	if cuts == nil || len(*cuts) == 0 || (*cuts)[0].n > t.seen+1 {
		return false
	}

	for _, c := range *cuts {
		if c.n > t.seen && c.n <= count && next > c.end {
			return false
		}
	}

	t.seen = count

	return true
}

// waitExclusive blocks until no truncation is in progress.
func (l *Lease) waitExclusive() {
	l.mu.Lock()
	for l.exclusive.Load() && !l.revoked.Load() {
		l.cond.Wait()
	}
	l.mu.Unlock()
}

// Release unregisters a reader previously registered with a successful Acquire.
func (l *Lease) Release() {
	if l.reading.Add(-1) != 0 || (!l.revoked.Load() && !l.exclusive.Load()) {
		return
	}

	// last reader while Revoke or Truncate waits: wake it. Taking the mutex orders
	// the broadcast after its check, so the wakeup cannot be lost.
	l.mu.Lock()
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Revoked reports whether the lease has been revoked.
func (l *Lease) Revoked() bool {
	return l.revoked.Load()
}

// Revoke prevents further acquisitions and blocks until all current readers
// have released.
func (l *Lease) Revoke() {
	l.revoked.Store(true)

	if l.reading.Load() == 0 {
		return
	}

	l.mu.Lock()
	for l.reading.Load() > 0 {
		l.cond.Wait()
	}
	l.mu.Unlock()
}

// Truncate runs fn while there are no readers, and keeps new ones out until it returns.
// end is the sequence that follows the last one that is kept: a scanner that
// read up to or beyond it, is rejected by AcquireAt from now on. The cut is also
// recorded when fn fails, since what it wrote is then unknown.
func (l *Lease) Truncate(end uint64, fn func() error) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.exclusive.Store(true)

	for l.reading.Load() > 0 {
		l.cond.Wait()
	}

	err := fn()

	l.recordCut(end)
	l.exclusive.Store(false)
	l.cond.Broadcast()

	return err
}

// recordCut adds a cut. Only called by Truncate.
func (l *Lease) recordCut(end uint64) {
	var old []cut
	if p := l.cuts.Load(); p != nil {
		old = *p
	}

	if len(old) >= cutRing {
		old = old[len(old)-cutRing+1:]
	}

	cuts := make([]cut, len(old), len(old)+1)
	copy(cuts, old)
	cuts = append(cuts, cut{n: l.cutCount.Load() + 1, end: end})

	l.cuts.Store(&cuts)
	l.cutCount.Add(1)
}
