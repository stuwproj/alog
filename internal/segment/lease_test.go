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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLease_Acquire(t *testing.T) {
	t.Parallel()

	t.Run("succeeds until revoked", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		require.True(t, l.Acquire())
		l.Release()

		l.Revoke()
		require.True(t, l.Revoked())
		require.False(t, l.Acquire())
	})
}

func TestLease_Revoke(t *testing.T) {
	t.Parallel()

	t.Run("waits for readers to release", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		require.True(t, l.Acquire())

		var done atomic.Bool
		go func() {
			l.Revoke()
			done.Store(true)
		}()

		time.Sleep(10 * time.Millisecond)
		require.False(t, done.Load())

		l.Release()
		require.Eventually(t, done.Load, time.Second, time.Millisecond)
	})
}

func TestLease(t *testing.T) {
	t.Parallel()

	t.Run("no reader is inside after revoke returns", func(t *testing.T) {
		t.Parallel()

		for range 200 {
			l := NewLease()

			var inside atomic.Int64
			var violated atomic.Bool
			var revokedDone atomic.Bool
			var wg sync.WaitGroup

			for range 8 {
				wg.Go(func() {
					for l.Acquire() {
						inside.Add(1)
						if revokedDone.Load() {
							violated.Store(true)
						}
						inside.Add(-1)
						l.Release()
					}
				})
			}

			l.Revoke()
			revokedDone.Store(true)

			require.Zero(t, inside.Load())
			wg.Wait()
			require.False(t, violated.Load())
		}
	})
}

func TestLease_Truncate(t *testing.T) {
	t.Parallel()

	t.Run("runs the function without readers", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		require.True(t, l.Acquire())

		var readers int64
		done := make(chan error, 1)

		go func() {
			done <- l.Truncate(5, func() error {
				readers = l.reading.Load()
				return nil
			})
		}()

		select {
		case <-done:
			t.Fatal("Truncate returned while a reader was active")
		case <-time.After(20 * time.Millisecond):
		}

		l.Release()
		require.NoError(t, <-done)
		require.Zero(t, readers)
	})

	t.Run("keeps new readers out until it is done, and then lets them in", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		inside := make(chan struct{})
		proceed := make(chan struct{})
		truncated := make(chan struct{})

		go func() {
			_ = l.Truncate(5, func() error {
				close(inside)
				<-proceed
				return nil
			})
			close(truncated)
		}()

		<-inside

		acquired := make(chan bool, 1)

		go func() { acquired <- l.Acquire() }()

		select {
		case <-acquired:
			t.Fatal("a reader got in during the truncation")
		case <-time.After(20 * time.Millisecond):
		}

		close(proceed)
		<-truncated
		require.True(t, <-acquired)
		l.Release()
	})

	t.Run("returns the error of the function and still records the cut", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		tok := l.NewToken()
		boom := errors.New("boom")

		require.ErrorIs(t, l.Truncate(3, func() error { return boom }), boom)
		require.False(t, l.AcquireAt(&tok, 4))
	})

	t.Run("a revoked lease releases a waiting reader", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		inside := make(chan struct{})
		proceed := make(chan struct{})

		go func() {
			_ = l.Truncate(5, func() error {
				close(inside)
				<-proceed
				return nil
			})
		}()

		<-inside

		acquired := make(chan bool, 1)

		go func() { acquired <- l.Acquire() }()

		l.Revoke()
		close(proceed)
		require.False(t, <-acquired)
	})
}

func TestLease_AcquireAt(t *testing.T) {
	t.Parallel()

	cut := func(l *Lease, end uint64) {
		require.NoError(t, l.Truncate(end, func() error { return nil }))
	}

	t.Run("accepts every position when nothing was cut", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		tok := l.NewToken()

		require.True(t, l.AcquireAt(&tok, 1_000_000))
		l.Release()
	})

	t.Run("rejects a scanner that read beyond the cut, and accepts one that did not", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		beyond, atCut, before := l.NewToken(), l.NewToken(), l.NewToken()

		cut(l, 10)

		require.False(t, l.AcquireAt(&beyond, 11))
		require.True(t, l.AcquireAt(&atCut, 10))
		l.Release()
		require.True(t, l.AcquireAt(&before, 4))
		l.Release()
	})

	t.Run("a scanner created after the cut is not affected by it", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		cut(l, 10)

		tok := l.NewToken()
		require.True(t, l.AcquireAt(&tok, 500))
		l.Release()
	})

	t.Run("checks every cut since the token was last used", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		tok := l.NewToken()

		cut(l, 50)
		cut(l, 20)
		cut(l, 80)

		require.False(t, l.AcquireAt(&tok, 21), "the second cut was 20")

		tok = l.NewToken()
		cut(l, 20)
		cut(l, 80)
		require.True(t, l.AcquireAt(&tok, 20))
		l.Release()
		require.True(t, l.AcquireAt(&tok, 21), "the token is up to date, and the cut before that is behind it")
		l.Release()
	})

	t.Run("a token is only moved forward when it is valid", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		tok := l.NewToken()
		cut(l, 10)

		require.False(t, l.AcquireAt(&tok, 20))
		require.False(t, l.AcquireAt(&tok, 20), "still invalid")
		require.True(t, l.AcquireAt(&tok, 5), "a seek back is fine")
		l.Release()
	})

	t.Run("a token that is older than the remembered cuts is rejected", func(t *testing.T) {
		t.Parallel()

		l := NewLease()
		tok := l.NewToken()

		for range cutRing + 2 {
			cut(l, 1000)
		}

		require.False(t, l.AcquireAt(&tok, 0))
	})
}
