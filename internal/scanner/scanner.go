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

package scanner

import (
	"errors"
	"io"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/storage"
	framecore "stuw.net/frame"
)

// NoLimit reads every payload completely.
const NoLimit = framecore.MaxPayloadSize

// ErrInvalidSequence is returned when a sequence number is invalid or out of range.
var ErrInvalidSequence = errors.New("invalid sequence")

type (
	// Validator aliases framecore.Validator specialized for uint32 length and frame.Header types.
	Validator = framecore.Validator[uint32, frame.Header]

	// Loader aliases framecore.Loader specialized for uint32 length and frame.Header types.
	Loader = framecore.Loader[uint32, frame.Header]

	// FrameScanner is an alias for framecore.Scanner specialized for uint32 length and frame.Header types.
	FrameScanner = framecore.Scanner[uint32, frame.Header]

	// ReaderLoader is an alias for framecore.ReaderLoader specialized for uint32 length and frame.Header types.
	ReaderLoader = framecore.ReaderLoader[uint32, frame.Header]

	// SliceLoader is an alias for framecore.SliceLoader specialized for uint32 length and frame.Header types.
	SliceLoader = framecore.SliceLoader[uint32, frame.Header]

	// MMapRegion is an alias for framecore.MMapRegion
	MMapRegion = framecore.MMapRegion
)

// CompositeValidator is a Validator that combines multiple validators and applies them in sequence.
type CompositeValidator struct {
	validators []Validator
}

// NewCompositeValidator creates a new CompositeValidator with the provided validators.
func NewCompositeValidator(validators ...Validator) *CompositeValidator {
	return &CompositeValidator{
		validators: validators,
	}
}

// Validate applies each validator in the CompositeValidator to the given frame header and payload.
func (v *CompositeValidator) Validate(h *frame.Header, payload []byte) error {
	for _, validator := range v.validators {
		if err := validator.Validate(h, payload); err != nil {
			return err
		}
	}

	return nil
}

// PreallocFrameValidator is a Validator that returns EOF errors
// for frames that are EOF markers or do not match the expected generation.
// Headers that were never written, or were zeroed, are reported as io.ErrUnexpectedEOF.
type PreallocFrameValidator struct {
	gen uint16
}

// NewPreallocFrameValidator creates a new PreallocFrameValidator with the specified generation.
func NewPreallocFrameValidator(gen uint16) *PreallocFrameValidator {
	return &PreallocFrameValidator{
		gen: gen,
	}
}

// Validate checks if the frame header is an EOF marker or if its generation does not
// match the expected generation.
func (v *PreallocFrameValidator) Validate(h *frame.Header, _ []byte) error {
	if err := h.CheckFlags(); err != nil {
		return err
	}

	if h.IsEOF() {
		return io.EOF
	}

	if h.Generation() != v.gen {
		return io.ErrUnexpectedEOF
	}

	return nil
}

// ChecksumValidator is a Validator that checks the checksum of the frame payload.
type ChecksumValidator struct {
	v framecore.CRC32C[frame.Header]
	p PreallocFrameValidator
}

// NewChecksumValidator creates a checksum validator that enforces
// generation/EOF semantics for the provided frame generation.
func NewChecksumValidator(gen uint16) *ChecksumValidator {
	return &ChecksumValidator{p: PreallocFrameValidator{gen: gen}}
}

// Validate checks the frame header and payload for validity, including checksum verification.
// EOF frames must carry a valid header checksum to end the scan.
func (v *ChecksumValidator) Validate(h *frame.Header, payload []byte) error {
	if h.IsEOF() && h.Generation() == v.p.gen {
		if h.Checksum() != framecore.Checksum(h, nil, 0) {
			return frame.ErrInvalidFrameHeader
		}
	}

	if err := v.p.Validate(h, payload); err != nil {
		return err
	}

	return v.v.Validate(h, payload)
}

// Scanner is a scanner that scanns frames from a log segment, starting from a base sequence number.
type Scanner struct {
	*FrameScanner
	BaseSeq uint64
}

// SeekSequence seeks to the specified sequence number in the log segment.
// It returns an error if the sequence number is invalid or out of range.
func (s *Scanner) SeekSequence(seq uint64) error {
	idx := seq - s.BaseSeq
	if idx > uint64(^uint32(0)) {
		return ErrInvalidSequence
	}

	return s.SeekIndex(uint32(idx))
}

// NextSequence returns the next sequence number in the log segment.
func (s *Scanner) NextSequence() uint64 {
	return s.BaseSeq + uint64(s.NextIndex)
}

// LastSequence returns the last scanned sequence number.
func (s *Scanner) LastSequence() uint64 {
	return s.BaseSeq + uint64(s.LastIndex)
}

// OpenMMap opens a memory-mapped region for the given file and size.
func OpenMMap(file storage.File) (*MMapRegion, error) {
	return framecore.OpenMMapRegion(file)
}
