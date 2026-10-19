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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLazyTimer(t *testing.T) {
	t.Parallel()

	tmr := newLazyTimer(10 * time.Millisecond)
	tmr.Start()

	select {
	case <-tmr.C:
	case <-time.After(time.Second):
		t.Fatal("timer did not fire")
	}

	t.Run("after Stop restart fires", func(t *testing.T) {
		t.Parallel()

		tmr := newLazyTimer(10 * time.Millisecond)
		tmr.Start()
		tmr.Stop()
		tmr.Start()

		select {
		case <-tmr.C:
		case <-time.After(time.Second):
			t.Fatal("timer did not fire after restart")
		}

		tmr.Stop()
		require.True(t, true)
	})
}
