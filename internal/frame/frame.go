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

package frame

import (
	"errors"

	framecore "stuw.net/frame"
)

const (
	// HeaderSize is the size of the frame header in bytes.
	HeaderSize = 16

	// HeadersOnly is a flag indicating that only headers should be scanned, without reading the payloads.
	HeadersOnly = framecore.HeadersOnly
)

const (
	// DefaultPoolDefaultSize is the default size of the frame pool in bytes.
	DefaultPoolDefaultSize = 4 * 1024

	// DefaultPoolMaxSize is the maximum size of the frame pool in bytes.
	DefaultPoolMaxSize = 1 * 1024 * 1024

	// DefaultPoolWarmup is the number of frames to preallocate in the pool.
	DefaultPoolWarmup = 3

	// DefaultPoolThreshold is the threshold for when to start reusing frames from the pool.
	DefaultPoolThreshold = 256

	// DefaultPoolRatio is the ratio of the pool size to the maximum segment size.
	DefaultPoolRatio = 0.5
)

var (
	// ErrPayloadTooLarge is returned when a payload is larger than allowed.
	ErrPayloadTooLarge = framecore.ErrPayloadTooLarge

	// ErrBatchTooLarge is returned when a batch cannot be written as one: it has too
	// many records, needs more buffers than one write takes, or cannot fit in an empty segment.
	ErrBatchTooLarge = errors.New("batch too large")

	// ErrInvalidChecksum is returned when the checksum is invalid.
	ErrInvalidChecksum = framecore.ErrInvalidChecksum

	// ErrInvalidFrameIndex is returned when the frame index is invalid.
	ErrInvalidFrameIndex = framecore.ErrInvalidFrameIndex

	// ErrInvalidFrameHeader is returned when the frame header is invalid.
	ErrInvalidFrameHeader = framecore.ErrInvalidFrameHeader
)

type (
	// Frame is a frame that can be read from or written to a storage file.
	Frame = framecore.Frame[uint32, Header]
)

// BatchSize returns the total size of a batch of payloads, including headers.
func BatchSize(batch []MultiPartPayload) int64 {
	size := int64(HeaderSize)

	for _, payload := range batch {
		size += HeaderSize
		size += payload.Size()
	}

	if len(batch) > 1 {
		size += HeaderSize
	}

	return size
}

// MultiPartPayload is a payload that can be split into multiple parts.
type MultiPartPayload [][]byte

// Size returns the total size of the payload, including all parts.
func (p MultiPartPayload) Size() int64 {
	var length int64
	for _, part := range p {
		length += int64(len(part))
	}

	return length
}

// Bytes returns the payload as a single byte slice, concatenating all parts.
func (p MultiPartPayload) Bytes() []byte {
	buf := make([]byte, 0, p.Size())
	for _, part := range p {
		buf = append(buf, part...)
	}

	return buf
}

// NewPayload creates a new MultiPartPayload from the given parts.
func NewPayload(parts ...[]byte) MultiPartPayload {
	return parts
}
