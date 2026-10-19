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

package checkpoint

import (
	"testing"

	"stuw.net/alog/internal/frame"
	"github.com/stretchr/testify/require"
)

func TestTracker_Validate(t *testing.T) {
	t.Parallel()

	t.Run("validates tracked checkpoint", func(t *testing.T) {
		t.Parallel()

		var cp frame.Header
		cp.SetCheckpoint(7, 4, 0, 0)

		err := (&Tracker{}).Validate(&cp, nil)
		require.NoError(t, err)
	})

	t.Run("rejects corrupted checkpoint header", func(t *testing.T) {
		t.Parallel()

		var cp frame.Header
		cp.SetCheckpoint(3, 16, 123, 0)
		cp[0] ^= 0xFF

		err := (&Tracker{}).Validate(&cp, nil)
		require.ErrorIs(t, err, frame.ErrInvalidFrameHeader)
	})

	t.Run("rejects checkpoint checksum mismatch", func(t *testing.T) {
		t.Parallel()

		validator := &Tracker{CRC: 99}

		var cp frame.Header
		cp.SetCheckpoint(2, 8, 100, 0)

		err := validator.Validate(&cp, nil)
		require.ErrorIs(t, err, frame.ErrInvalidChecksum)
	})
}

func TestTracker_PredictCheckpoints(t *testing.T) {
	t.Parallel()

	t.Run("one checkpoint when the batch completes the stride", func(t *testing.T) {
		t.Parallel()

		tr := NewTracker(4)
		require.Equal(t, 0, tr.PredictCheckpoints(3))
		require.Equal(t, 1, tr.PredictCheckpoints(4))
	})

	t.Run("a batch exceeding the stride still gets one checkpoint", func(t *testing.T) {
		t.Parallel()

		tr := NewTracker(4)
		require.Equal(t, 1, tr.PredictCheckpoints(10))
	})

	t.Run("accounts for frames tracked before", func(t *testing.T) {
		t.Parallel()

		tr := NewTracker(4)

		var h frame.Header
		h.SetOne(0, 1, []byte("x"))
		tr.Track(&h)
		tr.Track(&h)

		require.Equal(t, 0, tr.PredictCheckpoints(1))
		require.Equal(t, 1, tr.PredictCheckpoints(2))
	})

	t.Run("none without a stride or frames", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, 0, NewTracker(0).PredictCheckpoints(10))
		require.Equal(t, 0, NewTracker(4).PredictCheckpoints(0))
	})

	t.Run("matches what the writer does", func(t *testing.T) {
		t.Parallel()

		for stride := uint16(1); stride <= 5; stride++ {
			for n := 1; n <= 12; n++ {
				tr := NewTracker(stride)

				var h frame.Header
				h.SetOne(0, 1, []byte("x"))

				predicted := tr.PredictCheckpoints(n)

				for range n {
					tr.Track(&h)
				}

				actual := 0
				if tr.ShouldCheckpoint() {
					actual = 1
				}

				require.Equal(t, actual, predicted, "stride %d batch %d", stride, n)
			}
		}
	})
}
