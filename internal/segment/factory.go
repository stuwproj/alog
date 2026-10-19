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
//

package segment

import (
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/storage"
)

// Factory builds segments with shared configuration.
type Factory struct {
	prealloc         int64
	checkpointStride uint16
	syncOnAppend     bool
}

// NewFactory creates a segment factory with shared construction settings.
func NewFactory(
	prealloc int64,
	syncOnAppend bool,
	checkpointStride uint16,
) *Factory {
	if prealloc == 0 {
		prealloc = 128 * 1024 * 1024 // default preallocation size of 128MB.
	}

	return &Factory{
		prealloc:         prealloc,
		syncOnAppend:     syncOnAppend,
		checkpointStride: checkpointStride,
	}
}

// New creates a segment with the given variable state.
func (f *Factory) New(file storage.File, meta header.MetaSlot, startOffset int64) *Segment {
	segment := newSegment(file, &meta, f.prealloc, f.syncOnAppend, f.checkpointStride)
	segment.startOffset = max(startOffset, int64(header.HeaderSize))
	segment.publish()

	return segment
}

// PreallocateSize returns the preallocation size for segments created by this factory.
func (f *Factory) PreallocateSize() int64 {
	return f.prealloc
}
