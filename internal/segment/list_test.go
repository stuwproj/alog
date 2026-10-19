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
	"testing"

	"stuw.net/alog/internal/header"
	"github.com/stretchr/testify/require"
)

func TestList_Search(t *testing.T) {
	t.Parallel()

	segs := createListSegments(t, []uint64{100, 200, 300})

	type table struct {
		name      string
		expectNil bool
		sequence  uint64
		cursor    int
		start     uint64
	}

	tests := []table{
		{name: "find head at start", sequence: 100, cursor: 0, start: 100},
		{name: "find head in middle", sequence: 150, cursor: 0, start: 100},
		{name: "find head at end", sequence: 199, cursor: 0, start: 100},

		{name: "find middle at start", sequence: 200, cursor: 1, start: 200},
		{name: "find middle in middle", sequence: 250, cursor: 1, start: 200},
		{name: "find middle at end", sequence: 299, cursor: 1, start: 200},

		{name: "find tail at start", sequence: 300, cursor: 2, start: 300},
		{name: "find tail in middle", sequence: 350, cursor: 2, start: 300},
		{name: "find tail at end", sequence: 399, cursor: 2, start: 300},

		// doesn't move cursor
		{name: "not found after tail", sequence: 400, cursor: 0, expectNil: true},
		{name: "not found before head", sequence: 99, cursor: 0, expectNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewList(segs)
			s := l.Search(tt.sequence)

			if tt.expectNil {
				require.Nil(t, s)
				return
			}

			require.NotNil(t, s)
			require.Equal(t, tt.start, s.StartSequence())
		})
	}

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		l := NewList(nil)
		require.Nil(t, l.Search(0))
	})

	t.Run("validates contiguous ordering", func(t *testing.T) {
		t.Parallel()

		segs := createListSegments(t, []uint64{0, 100, 200})
		require.NoError(t, validateContiguous(segs))

		broken := createListSegments(t, []uint64{0, 150})
		err := validateContiguous(broken)
		require.Error(t, err)
		require.Contains(t, err.Error(), "segments are not contiguous")
	})
}

func createListSegments(t *testing.T, starts []uint64) []*Segment {
	t.Helper()

	segments := make([]*Segment, 0, len(starts))

	for _, start := range starts {
		h := header.NewHeader(start, 0)
		m, err := h.Meta()
		require.NoError(t, err)

		segments = append(segments, &Segment{
			meta:           &m,
			nextWriteIndex: 100,
		})
	}

	return segments
}
