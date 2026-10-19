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

package checkpoint

import (
	"fmt"
	"hash/crc32"

	"stuw.net/alog/internal/frame"
	modframe "stuw.net/frame"
)

var (
	// ErrInvalidCheckpoint is returned when a checkpoint frame fails validation.
	ErrInvalidCheckpoint = fmt.Errorf("checkpoint validation failed: %w", frame.ErrInvalidChecksum)

	// ErrInvalidEOF is returned when an EOF frame fails validation.
	ErrInvalidEOF = fmt.Errorf("eof validation failed: %w", frame.ErrInvalidChecksum)
)

// Tracker accumulates the checksum of frames
// and creates checkpoint frames.
type Tracker struct {
	CRC       uint32
	stride    uint16
	Remaining uint16
}

// State captures tracker resume state.
type State struct {
	CRC       uint32
	Remaining uint16
}

// Reset resets the tracker to its initial state.
func (s *Tracker) Reset() {
	s.CRC = 0
	s.Remaining = s.stride
}

// SetState restores tracker checksum/remaining state.
func (s *Tracker) SetState(crc uint32, remaining uint16) {
	s.CRC = crc
	s.Remaining = remaining
}

// NewTracker creates a new CheckpointSummer with the given stride.
func NewTracker(stride uint16) *Tracker {
	return &Tracker{
		stride:    stride,
		Remaining: stride,
	}
}

// Validate replays a frame header read from the log into the tracker.
//
// Data frames are tracked exactly as they were when written. Checkpoint frames,
// including EOF frames, are verified against the accumulated checksum.
// A regular checkpoint resets the tracker; an EOF frame does not, because
// appends continue from the state preceding the EOF frame.
func (s *Tracker) Validate(h *frame.Header, _ []byte) error {
	if !h.IsCheckpoint() {
		s.Track(h)
		return nil
	}

	// validate the checkpoint header itself
	crc := crc32.Update(0, modframe.CRC32cTable, h[:h.ChecksumBytes()])
	if h.Checksum() != crc {
		return frame.ErrInvalidFrameHeader
	}

	if h.CheckpointChecksum() != s.CRC {
		if h.IsEOF() {
			return ErrInvalidEOF
		}

		return ErrInvalidCheckpoint
	}

	if !h.IsEOF() {
		s.CRC = 0
		s.Remaining = s.stride
	}

	return nil
}

// State returns the current tracker state.
func (s *Tracker) State() State {
	return State{CRC: s.CRC, Remaining: s.Remaining}
}

// NewEOF returns an EOF checkpoint frame for the given generation and state.
func NewEOF(generation, stride uint16, state State) (h frame.Header) {
	h.SetCheckpoint(generation, stride, state.CRC, frame.FlagEOF)
	return h
}

// EOF populates the given frame header with the current checksum without
// resetting the tracker.
func (s *Tracker) EOF(generation uint16, cph *frame.Header) {
	cph.SetCheckpoint(generation, s.stride, s.CRC, frame.FlagEOF)
}

// Checkpoint pupulates the given frame header with the current checksum and resets the tracker.
func (s *Tracker) Checkpoint(generation uint16, cph *frame.Header) {
	if s.stride == 0 {
		return
	}

	if s.Remaining != 0 {
		panic("checkpoint called before stride completed")
	}

	cph.SetCheckpoint(generation, s.stride, s.CRC, 0)

	s.Remaining = s.stride
	s.CRC = 0
}

// Track accumulates the checksum of the given batch and tracks the number of remaining frames
// until the next checkpoint.
func (s *Tracker) Track(hdr *frame.Header) {
	s.CRC = crc32.Update(s.CRC, modframe.CRC32cTable, hdr[:])

	if s.stride == 0 {
		return
	}

	if s.Remaining > 0 {
		s.Remaining--
	}
}

// ShouldCheckpoint returns true if the tracker has completed a stride
// and is ready to create a checkpoint.
func (s *Tracker) ShouldCheckpoint() bool {
	return s.stride > 0 && s.Remaining == 0
}

// Stride returns the configured checkpoint stride.
func (s *Tracker) Stride() uint16 {
	return s.stride
}

// PredictCheckpoints returns how many checkpoint frames would be emitted
// when tracking the next n data frames as one batch.
//
// Checkpoints are only emitted at batch boundaries, so a batch that completes
// the stride (or exceeds it) is followed by exactly one checkpoint.
func (s *Tracker) PredictCheckpoints(n int) int {
	if n <= 0 || s.stride == 0 {
		return 0
	}

	if int(s.Remaining) <= n {
		return 1
	}

	return 0
}
