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

package header

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

const (
	// FlagCheckpointStateInitialized indicates checkpoint bootstrap state is persisted.
	FlagCheckpointStateInitialized uint16 = 1 << 1
)

// MetaSlot is a 32-byte big-endian encoded array representing a segment metadata slot.
//
// offset  size  field
// 0       8     start sequence
// 8       8     read byte offset
// 16      2     slot generation
// 18      2     frame generation
// 20      2     flags
// 22      2     checkpoint remaining
// 24      4     checkpoint crc
// 28      4     slot crc
type MetaSlot [32]byte

// Flags returns the flags from the metadata slot.
func (m MetaSlot) Flags() uint16 {
	return binary.BigEndian.Uint16(m[20:22])
}

// CheckpointRemaining returns tracker remaining state at the read boundary.
func (m MetaSlot) CheckpointRemaining() uint16 {
	return binary.BigEndian.Uint16(m[22:24])
}

// CheckpointCRC returns tracker checksum state at the read boundary.
func (m MetaSlot) CheckpointCRC() uint32 {
	return binary.BigEndian.Uint32(m[24:28])
}

func (m *MetaSlot) updateCheckSum() {
	binary.BigEndian.PutUint32(m[28:32], m.Checksum())
}

// Update the metadata slot with the provided values.
func (m *MetaSlot) Update(
	seq uint64,
	offset int64,
	frameGeneration, flags, remaining uint16,
	checkpointCRC uint32,
) {
	if offset < 0 {
		panic(fmt.Sprintf("negative read offset: %d", offset))
	}

	v := *m

	binary.BigEndian.PutUint64(v[0:8], seq)
	binary.BigEndian.PutUint64(v[8:16], uint64(offset))
	binary.BigEndian.PutUint16(v[16:18], v.Generation()+1)
	binary.BigEndian.PutUint16(v[18:20], frameGeneration)
	binary.BigEndian.PutUint16(v[20:22], flags)
	binary.BigEndian.PutUint16(v[22:24], remaining)
	binary.BigEndian.PutUint32(v[24:28], checkpointCRC)
	binary.BigEndian.PutUint32(v[28:32], v.Checksum())

	*m = v
}

// With creates a new MetaSlot with mutated values.
func (m MetaSlot) With(
	seq uint64,
	offset int64,
	frameGeneration, flags, remaining uint16,
	checkpointCRC uint32,
) MetaSlot {
	newSlot := m
	newSlot.Update(seq, offset, frameGeneration, flags, remaining, checkpointCRC)

	return newSlot
}

// StartSequence returns the start sequence number from the metadata slot.
func (m MetaSlot) StartSequence() uint64 {
	return binary.BigEndian.Uint64(m[0:8])
}

// ReadOffset returns the read offset from the metadata slot.
func (m MetaSlot) ReadOffset() int64 {
	return int64(binary.BigEndian.Uint64(m[8:16])) // #nosec G115 set as int64
}

// Generation returns the metadata slot generation number.
func (m MetaSlot) Generation() uint16 {
	return binary.BigEndian.Uint16(m[16:18])
}

// FrameGeneration returns the frame generation for a v2 metadata slot.
func (m MetaSlot) FrameGeneration() uint16 {
	return binary.BigEndian.Uint16(m[18:20])
}

// CRC returns the stored CRC checksum from the metadata slot.
func (m MetaSlot) CRC() uint32 {
	return binary.BigEndian.Uint32(m[28:32])
}

// Checksum calculates the CRC checksum for the metadata slot.
func (m MetaSlot) Checksum() uint32 {
	return crc32.Checksum(m[0:28], crc32cTable)
}

// Valid checks if the metadata slot is valid by comparing the stored CRC with the calculated checksum.
func (m MetaSlot) Valid() bool {
	return m.Checksum() == m.CRC()
}

// NewerThan compares generation numbers as signed integers to handle wrap-around.
// #nosec G115 -- uint16 subtraction intentionally wraps; int16 sign interprets recency across wrap.
func (m MetaSlot) NewerThan(other MetaSlot) bool {
	return int16(m.Generation()-other.Generation()) > 0
}
