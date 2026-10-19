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
	"sort"
)

func validateContiguous(segments []*Segment) error {
	for i := 1; i < len(segments); i++ {
		prev := segments[i-1]
		curr := segments[i]
		if prev.NextSequence() != curr.StartSequence() {
			return fmt.Errorf(
				"segments are not contiguous: segment %d ends at %d, but segment %d starts at %d",
				i-1,
				prev.NextSequence(),
				i,
				curr.StartSequence(),
			)
		}
	}

	return nil
}

// List is a slice of segments that can be searched for a segment
// containing a given sequence number. It is not safe for concurrent use: the chain
// changes it while holding its lock, and scanners use a Snapshot.
type List struct {
	segments []*Segment
}

// NewList creates a new List with the given segments.
func NewList(segments []*Segment) *List {
	return &List{
		segments: segments,
	}
}

// Add a segment to the list.
func (l *List) Add(segment *Segment) {
	l.segments = append(l.segments, segment)
}

// Pop removes the last segment of the list and returns it, or returns nil if the list is empty.
func (l *List) Pop() *Segment {
	if len(l.segments) == 0 {
		return nil
	}

	last := l.segments[len(l.segments)-1]
	l.segments[len(l.segments)-1] = nil
	l.segments = l.segments[:len(l.segments)-1]

	return last
}

// Shift removes and returns the first segment in the list, or nil if the list is empty.
func (l *List) Shift() *Segment {
	if len(l.segments) == 0 {
		return nil
	}

	seg := l.segments[0]
	l.segments[0] = nil
	l.segments = l.segments[1:]

	return seg
}

// Unshift puts a segment in front of the list.
func (l *List) Unshift(segment *Segment) {
	l.segments = append([]*Segment{segment}, l.segments...)
}

// Slice returns the underlying slice of segments.
func (l *List) Slice() []*Segment {
	return l.segments
}

// Head returns the first segment in the list, or nil if the list is empty.
func (l *List) Head() *Segment {
	if len(l.segments) == 0 {
		return nil
	}

	return l.segments[0]
}

// Tail returns the last segment in the list, or nil if the list is empty.
func (l *List) Tail() *Segment {
	if len(l.segments) == 0 {
		return nil
	}

	return l.segments[len(l.segments)-1]
}

// Clone returns a copy of the list with the same segments.
func (l *List) Clone() *List {
	segments := make([]*Segment, len(l.segments))
	copy(segments, l.segments)

	return NewList(segments)
}

// Len returns the number of segments in the list.
func (l *List) Len() int {
	return len(l.segments)
}

// Sort sorts the segments in ascending order by their start sequence number.
func (l *List) Sort() {
	sort.Slice(l.segments, func(i, j int) bool {
		return l.segments[i].StartSequence() < l.segments[j].StartSequence()
	})
}

// Search returns the segment that contains the given sequence number, or nil if
// no such segment exists. Assumes the segments are sorted.
func (l *List) Search(seq uint64) *Segment {
	if len(l.segments) == 0 {
		return nil
	}

	// if the list starts after the given sequence number, return nil
	if l.segments[0].StartSequence() > seq {
		return nil
	}

	// find the first segment that ends after the given sequence number
	idx := sort.Search(len(l.segments), func(i int) bool {
		next := l.segments[i].NextSequence()
		if next == 0 {
			return false
		}

		return next > seq
	})

	// if seq is the next sequence number of the last segment, return nil
	if idx == len(l.segments) {
		return nil
	}

	return l.segments[idx]
}
