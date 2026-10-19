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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/scanner"
	"stuw.net/alog/internal/storage"
)

const indexSliceCapacity = 256

var (
	// ErrInvalidTailFrame is returned when the last frame in the segment is invalid or corrupted.
	ErrInvalidTailFrame = errors.New("invalid tail frame")

	// ErrTornFrame is returned when a torn frame is detected before the tail of the segment.
	ErrTornFrame = errors.New("tail torn frame detected before tail")
)

// Segment represents a segment in the append-only log.
// It should not be used concurrently by multiple goroutines.
type Segment struct {
	file           storage.File
	failed         error
	meta           *header.MetaSlot
	logger         func(string)
	tracker        *checkpoint.Tracker
	offsetIndex    *checkpoint.Index
	mmap           *scanner.MMapRegion
	lease          *Lease
	pubRead        atomic.Pointer[readPosition]
	bufs           [][]byte
	hdrs           []frame.Header
	writeOffset    int64
	published      atomic.Int64
	pubNext        atomic.Uint64
	prealloc       int64
	startOffset    int64
	cpOffset       int64
	mmapMu         sync.Mutex
	nextWriteIndex uint32
	cpIndex        uint32
	closed         atomic.Bool
	frameGen       uint16
	syncOnAppend   bool
	cpPending      bool
}

// Name returns the name of the segment file.
func (s *Segment) Name() string {
	return s.file.Name()
}

// ProjectedSize returns the projected size of the segment after
// appending the given payloads.
func (s *Segment) ProjectedSize(batch ...frame.MultiPartPayload) (int64, error) {
	n, err := s.AppendSize(batch...)
	return s.WriteOffset() + n, err
}

// DiskSize returns the physical size represented by this segment.
func (s *Segment) DiskSize() int64 {
	return max(s.writeOffset, s.prealloc)
}

// Published returns the offset up to which scanners may read. Everything below it
// was validated when the segment was loaded, or completely written (and synced,
// if so configured) by a successful append. It is advanced after the write, so
// a scanner never sees a batch that is in flight, or one that is rolled back.
func (s *Segment) Published() int64 {
	return s.published.Load()
}

// readPosition is where reading the segment starts: the first sequence, and the offset of
// that record. They are published together, since a scanner needs both to agree.
type readPosition struct {
	start  uint64
	offset int64
}

// PublishedStart returns the first sequence of the segment, as it is visible to
// scanners. Unlike StartSequence it can be read while the segment is appended to.
func (s *Segment) PublishedStart() uint64 {
	return s.pubRead.Load().start
}

// PublishedNext returns the sequence that follows the last record that is visible to
// scanners. Unlike NextSequence it can be read while the segment is appended to.
func (s *Segment) PublishedNext() uint64 {
	return s.pubNext.Load()
}

// publish makes the current end and sequences of the segment visible to scanners.
// The offset goes first, so a scanner that sees a sequence can read its record.
// Only the goroutine that mutates the segment calls this.
func (s *Segment) publish() {
	s.published.Store(s.writeOffset)

	// only changes when the head is truncated, so an append does not allocate
	start, offset := s.meta.StartSequence(), s.startOffset
	if current := s.pubRead.Load(); current == nil || current.start != start || current.offset != offset {
		s.pubRead.Store(&readPosition{start: start, offset: offset})
	}

	s.pubNext.Store(s.NextSequence())
}

// WriteOffset returns the logical size represented by this segment.
func (s *Segment) WriteOffset() int64 {
	return s.writeOffset
}

// NextSequence returns the next sequence number that will be assigned to the next appended data.
func (s *Segment) NextSequence() uint64 {
	return s.meta.StartSequence() + uint64(s.nextWriteIndex)
}

// StartSequence returns the starting sequence number of the segment.
func (s *Segment) StartSequence() uint64 {
	return s.meta.StartSequence()
}

// Reset updates the segments header and internal state to the given base sequence, offset, and index.
func (s *Segment) Reset(baseSeq uint64, offset int64) error {
	if s.failed != nil {
		return s.failed
	}

	if offset < int64(header.HeaderSize) || offset > s.writeOffset {
		return header.ErrInvalidSegmentHeader
	}

	state, _, err := s.stateAt(offset)
	if err != nil {
		return err
	}

	if err := header.MoveReadPosition(s.file, header.ReadPosition{
		Seq:         baseSeq,
		ReadOffset:  offset,
		CPRemaining: state.Remaining,
		CPCRC:       state.CRC,
	}, s.meta); err != nil {
		return err
	}

	s.startOffset = offset

	return s.init()
}

// ReadStart returns the read offset of the segment
func (s *Segment) ReadStart() int64 {
	return s.startOffset
}

// Sync flushes file contents and metadata to stable storage.
func (s *Segment) Sync() error {
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync segment file: %w", err)
	}

	return nil
}

// Lease returns the read lease that guards access to this segment's mapped bytes.
func (s *Segment) Lease() *Lease {
	return s.lease
}

// Close revokes the read lease, waits for in-progress reads to finish,
// and then releases the mapping and file handle. Both are released, also when
// 1 of them fails. Closing a segment again does nothing.
func (s *Segment) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	s.lease.Revoke()

	s.mmapMu.Lock()
	mmap := s.mmap
	s.mmap = nil
	s.mmapMu.Unlock()

	var err error
	if mmap != nil {
		err = mmap.Close()
	}

	return errors.Join(err, s.file.Close())
}

func newSegment(
	file storage.File,
	m *header.MetaSlot,
	prealloc int64,
	syncOnAppend bool,
	checkpointStride uint16,
) *Segment {
	s := &Segment{
		file:         file,
		meta:         m,
		prealloc:     prealloc,
		offsetIndex:  checkpoint.NewIndex(indexSliceCapacity),
		lease:        NewLease(),
		writeOffset:  header.HeaderSize, // start after the header
		startOffset:  header.HeaderSize,
		syncOnAppend: syncOnAppend,
		tracker:      checkpoint.NewTracker(checkpointStride),
		hdrs:         make([]frame.Header, 16),
		bufs:         make([][]byte, 0),
		frameGen:     m.FrameGeneration(),
	}

	s.publish()

	return s
}
