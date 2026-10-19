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
	"fmt"
	"testing"

	"stuw.net/alog/internal/frame"
	"github.com/stretchr/testify/require"
)

func TestSnapshot_Locate(t *testing.T) {
	t.Parallel()

	// small segments, so 1 batch of 3 records fills 1
	chain := NewLoadedChain(t, 0, 200)
	t.Cleanup(func() { _ = chain.Close() })

	for i := range 12 {
		_, err := chain.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte(fmt.Sprintf("a%02d", i))),
			frame.NewPayload([]byte(fmt.Sprintf("b%02d", i))),
			frame.NewPayload([]byte(fmt.Sprintf("c%02d", i))),
		})
		require.NoError(t, err)
	}

	snapshot := chain.Snapshot()
	require.Greater(t, snapshot.Len(), 3)

	length := snapshot.Len()

	next := snapshot.Tail().PublishedNext()
	require.Equal(t, uint64(36), next)

	t.Run("every sequence is in the segment that holds it", func(t *testing.T) {
		for seq := range next {
			seg := snapshot.Locate(seq)
			require.NotNil(t, seg, "seq %d", seq)
			require.LessOrEqual(t, seg.PublishedStart(), seq)
			require.Greater(t, seg.PublishedNext(), seq)
		}
	})

	t.Run("the sequence after the last one is in no segment", func(t *testing.T) {
		require.Nil(t, snapshot.Locate(next))
		require.Nil(t, snapshot.Locate(next+100))
	})

	t.Run("the first sequence of a segment is in that segment", func(t *testing.T) {
		for i := range snapshot.Len() {
			seg := snapshot.At(i)
			require.Same(t, seg, snapshot.Locate(seg.PublishedStart()))
			require.Equal(t, i, snapshot.IndexOf(seg))
		}
	})

	t.Run("a sequence before the head is in no segment", func(t *testing.T) {
		require.NoError(t, chain.TruncateBefore(10))

		after := chain.Snapshot()
		require.Nil(t, after.Locate(0))
		require.NotNil(t, after.Locate(10))
		require.Equal(t, -1, after.IndexOf(snapshot.At(0)), "the removed segment is not in the new snapshot")
		require.Equal(t, length, snapshot.Len(), "the old snapshot is not modified")
	})
}
