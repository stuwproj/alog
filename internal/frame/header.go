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

package frame

import (
	"encoding/binary"
	"io"
	"math"

	"stuw.net/frame"
)

const (
	// FlagEOF marks the end of the log. EOF frames are always checkpoints.
	FlagEOF uint16 = 1 << 0

	// FlagCheckpoint indicates the header is a checkpoint header.
	FlagCheckpoint uint16 = 1 << 1

	// FlagData indicates the header belongs to a data frame.
	FlagData uint16 = 1 << 2

	// FlagBatchEnd marks the last data frame of an append batch.
	// Everything up to and including it was written by completed batches.
	FlagBatchEnd uint16 = 1 << 3
)

// Header is a 16-byte big-endian encoded array, representing a frame header.
//
// offset  size  field
// 0       4     payload length / checkpoint CRC32C
// 4       2     index stride / checkpoint stride
// 6       2     reserved
// 8       2     frame generation
// 10      2     flags
// 12      4     CRC32C
type Header [HeaderSize]byte

// IsBatchEnd returns true if the frame is the last data frame of an append batch.
func (h Header) IsBatchEnd() bool {
	return h.flags()&FlagBatchEnd != 0
}

// CheckFlags verifies that the flags describe a known kind of frame:
//
//	data:       FlagData, optionally with FlagBatchEnd
//	checkpoint: FlagCheckpoint
//	EOF:        FlagCheckpoint | FlagEOF
//
// A header without any flags was never written, or was zeroed, which is reported
// as io.ErrUnexpectedEOF. Any other combination is ErrInvalidFrameHeader.
func (h Header) CheckFlags() error {
	switch h.flags() {
	case FlagData, FlagData | FlagBatchEnd, FlagCheckpoint, FlagCheckpoint | FlagEOF:
		return nil
	case 0:
		return io.ErrUnexpectedEOF
	default:
		return ErrInvalidFrameHeader
	}
}

// IsEOF returns true if the frame is an EOF marker.
func (h Header) IsEOF() bool {
	return h.flags()&FlagEOF != 0
}

func (h Header) flags() uint16 {
	return binary.BigEndian.Uint16(h[10:12])
}

// Generation returns the generation from the frame header.
func (h Header) Generation() uint16 {
	return binary.BigEndian.Uint16(h[8:10])
}

// PayloadLength returns the payload length from the frame header.
func (h Header) PayloadLength() uint32 {
	if h.IsCheckpoint() || h.IsEOF() {
		return 0
	}

	return binary.BigEndian.Uint32(h[0:4])
}

// IsCheckpoint returns true if the frame is a checkpoint frame.
func (h Header) IsCheckpoint() bool {
	return h.flags()&FlagCheckpoint != 0
}

// CheckpointChecksum returns the checksum of frame headers since the previous checkpoint.
func (h Header) CheckpointChecksum() uint32 {
	if !h.IsCheckpoint() {
		return 0
	}

	return binary.BigEndian.Uint32(h[0:4])
}

// CheckpointStride returns the number of frames until the next checkpoint.
func (h Header) CheckpointStride() uint16 {
	if !h.IsCheckpoint() {
		return 0
	}

	return binary.BigEndian.Uint16(h[4:6])
}

// IndexStride returns the index stride from the frame header.
func (h Header) IndexStride() uint16 {
	if h.IsCheckpoint() || h.IsEOF() {
		return 0
	}

	return binary.BigEndian.Uint16(h[4:6])
}

// Checksum returns the CRC32C checksum from the frame header.
func (h Header) Checksum() uint32 {
	return binary.BigEndian.Uint32(h[12:16])
}

// ChecksumBytes returns the bytes of the header used for checksum calculation.
func (h Header) ChecksumBytes() int {
	return 12
}

// SetChecksum sets the CRC32C checksum in the frame header.
func (h *Header) SetChecksum(checksum uint32) {
	binary.BigEndian.PutUint32(h[12:16], checksum)
}

// SetCheckpoint sets the frame header fields for a checkpoint frame.
func (h *Header) SetCheckpoint(generation uint16, stride uint16, checkpointChecksum uint32, flags uint16) {
	binary.BigEndian.PutUint32(h[0:4], checkpointChecksum)
	binary.BigEndian.PutUint16(h[4:6], stride)
	binary.BigEndian.PutUint16(h[6:8], 0)
	binary.BigEndian.PutUint16(h[8:10], generation)

	flags |= FlagCheckpoint
	binary.BigEndian.PutUint16(h[10:12], flags)

	// set the checksum of the header fields for the checkpoint frame
	h.SetChecksum(frame.UpdateCRC32C(0, h[:h.ChecksumBytes()]))
}

// SetLength sets the frame header fields for a frame with the given generation, index stride, and payload length.
func (h *Header) SetLength(generation uint16, indexStride uint16, length uint32) {
	binary.BigEndian.PutUint32(h[0:4], length)
	binary.BigEndian.PutUint16(h[4:6], indexStride)
	binary.BigEndian.PutUint16(h[8:10], generation)
}

// Set the frame header fields of a data frame based on the given payload.
func (h *Header) Set(generation uint16, indexStride uint16, payload MultiPartPayload) {
	h.set(generation, indexStride, FlagData, payload)
}

// SetBatchEnd sets the frame header fields of the last data frame of a batch.
func (h *Header) SetBatchEnd(generation uint16, indexStride uint16, payload MultiPartPayload) {
	h.set(generation, indexStride, FlagData|FlagBatchEnd, payload)
}

func (h *Header) set(generation uint16, indexStride uint16, flags uint16, payload MultiPartPayload) {
	var payloadLen int
	for _, part := range payload {
		payloadLen += len(part)
	}

	if payloadLen > math.MaxUint32 {
		panic("payload length overflow")
	}

	binary.BigEndian.PutUint16(h[10:12], flags)

	// #nosec G115 -- payload length is validated by callers before header construction.
	h.SetLength(generation, indexStride, uint32(payloadLen))

	crc := frame.UpdateCRC32C(0, h[:h.ChecksumBytes()])

	for i := range payload {
		crc = frame.UpdateCRC32C(crc, payload[i])
	}

	h.SetChecksum(crc)
}

// SetOne sets the frame header fields of a data frame with a single-part payload.
func (h *Header) SetOne(generation uint16, indexStride uint16, payload []byte) {
	payloadLen := len(payload)
	if payloadLen > math.MaxUint32 {
		panic("payload length overflow")
	}

	binary.BigEndian.PutUint16(h[10:12], FlagData)
	h.SetLength(generation, indexStride, uint32(payloadLen))
	crc := frame.UpdateCRC32C(0, h[:h.ChecksumBytes()])
	crc = frame.UpdateCRC32C(crc, payload)
	h.SetChecksum(crc)
}
