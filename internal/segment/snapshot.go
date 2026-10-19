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
	"slices"
	"sort"
)

// Snapshot is an immutable view of the segments of a chain at some point, ordered
// by sequence. The chain publishes a new one whenever its segments change, so
// scanners can follow the chain without taking a lock. A segment in a snapshot
// may have been removed from the chain since. Its lease then is revoked.
type Snapshot struct {
	segments []*Segment
}

// Len returns the number of segments.
func (s *Snapshot) Len() int {
	return len(s.segments)
}

// At returns the segment at index i.
func (s *Snapshot) At(i int) *Segment {
	return s.segments[i]
}

// Head returns the first segment, or nil if there are none.
func (s *Snapshot) Head() *Segment {
	if len(s.segments) == 0 {
		return nil
	}

	return s.segments[0]
}

// Tail returns the last segment, or nil if there are none.
func (s *Snapshot) Tail() *Segment {
	if len(s.segments) == 0 {
		return nil
	}

	return s.segments[len(s.segments)-1]
}

// IndexOf returns the index of seg, or -1 if it is not in the snapshot.
func (s *Snapshot) IndexOf(seg *Segment) int {
	for i, candidate := range slices.Backward(s.segments) {
		if candidate == seg {
			return i
		}
	}

	return -1
}

// Locate returns the segment that contains seq, or nil if there is none: seq is
// before the first record, or at or after the next sequence of the tail.
// It only uses what the segments published, so it is safe to use while the log is
// appended to.
func (s *Snapshot) Locate(seq uint64) *Segment {
	idx := sort.Search(len(s.segments), func(i int) bool {
		return s.segments[i].PublishedNext() > seq
	})

	if idx == len(s.segments) || s.segments[idx].PublishedStart() > seq {
		return nil
	}

	return s.segments[idx]
}
