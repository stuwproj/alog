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

package header

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"stuw.net/alog/internal/storage"
)

const (
	// SegmentMagic is the magic number used to identify a segment file
	SegmentMagic uint32 = 0x414C4F47 // "ALOG"

	// SegmentVersion is the version of the segment file format
	SegmentVersion uint32 = 1

	// HeaderSize is the size of a segment header in bytes
	HeaderSize = 72

	metaSlot1Offset = 8
	metaSlot2Offset = 40
)

var (
	// ErrInvalidSegmentHeader is returned when a segment header is invalid
	ErrInvalidSegmentHeader = errors.New("invalid segment header")

	// ErrFrameGenerationNotBumped is returned when a recycled segment does not get a new frame generation.
	ErrFrameGenerationNotBumped = errors.New("frame generation not bumped on recycle")
)

// Header is a 72-byte big-endian encoded array, representing a segment header.
//
// The segment header is designed for durability, with two copies of
// the writable metadata. This allows for recovery in case of corruption
// or partial writes.
//
// On load the segment will read both copies and use a valid segment with
// the highest generation number. On write the segment will durably write
// the unused copy with a generation number one higher than the current copy.
//
// offset  size  field
// 0       4     magic
// 4       4     version
//
// 8       8     start sequence 1
// 16      8     read byte offset 1
// 24      2     slot generation 1
// 26      2     frame generation 1
// 28      2     flags 1
// 30      2     checkpoint remaining 1
// 32      4     checkpoint crc 1
// 36      4     slot crc 1
//
// 40      8     start sequence 2
// 48      8     read byte offset 2
// 56      2     slot generation 2
// 58      2     frame generation 2
// 60      2     flags 2
// 62      2     checkpoint remaining 2
// 64      4     checkpoint crc 2
// 68      4     slot crc 2
type Header [72]byte

// Magic returns the magic number from the segment header
func (h Header) Magic() uint32 {
	return binary.BigEndian.Uint32(h[0:4])
}

// Version returns the format version from the segment header
func (h Header) Version() uint32 {
	v := binary.BigEndian.Uint32(h[4:8])
	if v == 0 {
		return 1
	}

	return v
}

func (h *Header) slot1() MetaSlot {
	return MetaSlot(h[8:40])
}

func (h *Header) slot2() MetaSlot {
	return MetaSlot(h[40:72])
}

// Valid checks if the magic and version headers match the
// library constants
func (h Header) Valid() bool {
	return h.Magic() == SegmentMagic &&
		h.Version() == SegmentVersion
}

// Meta returns the newest valid metadata from the segment header.
func (h Header) Meta() (MetaSlot, error) {
	a, b := h.slot1(), h.slot2()

	vA := a.Valid()
	vB := b.Valid()

	switch {
	case vA && vB:
		if a.NewerThan(b) {
			return a, nil
		}

		return b, nil

	case vA:
		return a, nil

	case vB:
		return b, nil

	default:
		return MetaSlot{}, ErrInvalidSegmentHeader
	}
}

// ReadPosition is what changes in a metadata slot when the start of the
// readable data moves: the start sequence, the read offset and the checkpoint
// state at that offset.
type ReadPosition struct {
	Seq         uint64
	ReadOffset  int64
	CPRemaining uint16
	CPCRC       uint32
}

// RecycleUpdate is the metadata of a recycled segment. Any value may change
// when a segment is recycled, which makes the frame generation bump mandatory.
type RecycleUpdate struct {
	Seq             uint64
	FrameGeneration uint16
	Flags           uint16
	CPRemaining     uint16
}

// MoveReadPosition persists a new read position using the dual metadata slots
// mechanism. Everything else in the slot, including the frame generation, is
// carried over. Only recycling is allowed to change the rest.
func MoveReadPosition(f storage.File, pos ReadPosition, slotPtr *MetaSlot) error {
	if pos.ReadOffset < 0 {
		return fmt.Errorf("negative read offset: %d", pos.ReadOffset)
	}

	slotVal := slotPtr.With(
		pos.Seq,
		pos.ReadOffset,
		slotPtr.FrameGeneration(),
		slotPtr.Flags()|FlagCheckpointStateInitialized,
		pos.CPRemaining,
		pos.CPCRC,
	)

	return writeSlot(f, slotVal, slotPtr)
}

// RecycleFile persists the metadata of a recycled segment. The slot generation
// continues from the current slot, so the recycled metadata is always the newest
// on load, and the frame generation must be exactly 1 higher than the current one.
func RecycleFile(f storage.File, update RecycleUpdate, slotPtr *MetaSlot) error {
	if update.FrameGeneration != slotPtr.FrameGeneration()+1 {
		return ErrFrameGenerationNotBumped
	}

	slotVal := slotPtr.With(
		update.Seq,
		0,
		update.FrameGeneration,
		update.Flags,
		update.CPRemaining,
		0,
	)

	return writeSlot(f, slotVal, slotPtr)
}

// writeSlot writes slotVal into the slot that does not hold the current
// metadata, syncs, and only then replaces the in-memory slot.
func writeSlot(f storage.File, slotVal MetaSlot, slotPtr *MetaSlot) error {
	headerOffset := int64(metaSlot1Offset)
	if slotPtr.Generation()%2 == 0 {
		headerOffset = int64(metaSlot2Offset)
	}

	if _, err := f.WriteAt(slotVal[:], headerOffset); err != nil {
		return err
	}

	if err := f.Sync(); err != nil {
		return err
	}

	*slotPtr = slotVal

	return nil
}

// NewHeader constructs a SegmentHeader for a new segment.
//
// offset  size  field
// 0       4     magic
// 4       4     version
//
// 8       8     start sequence 1
// 16      8     read byte offset 1
// 24      2     slot generation 1
// 26      2     frame generation 1
// 28      2     flags 1
// 30      2     checkpoint remaining 1
// 32      4     checkpoint crc 1
// 36      4     slot crc 1
//
// 40      8     start sequence 2
// 48      8     read byte offset 2
// 56      2     slot generation 2
// 58      2     frame generation 2
// 60      2     flags 2
// 62      2     checkpoint remaining 2
// 64      4     checkpoint crc 2
// 68      4     slot crc 2
func NewHeader(startSequence uint64, frameGeneration uint16) Header {
	var h Header

	binary.BigEndian.PutUint32(h[0:4], SegmentMagic)
	binary.BigEndian.PutUint32(h[4:8], SegmentVersion)

	m := h[8:40]
	binary.BigEndian.PutUint64(m[0:8], startSequence)
	binary.BigEndian.PutUint16(m[18:20], frameGeneration)
	binary.BigEndian.PutUint16(m[22:24], 0)
	binary.BigEndian.PutUint32(m[24:28], 0)
	binary.BigEndian.PutUint32(m[28:32], crc32.Checksum(m[0:28], crc32cTable))

	return h
}
