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
	"slices"
	"sync"
	"sync/atomic"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
)

// ErrSegmentFull is returned when the segment is full
// and a new one could not be created.
var (
	ErrSegmentFull   = errors.New("segment capacity exhausted")
	ErrDiskFull      = errors.New("max disk size exceeded")
	ErrBatchTooLarge = frame.ErrBatchTooLarge
	ErrOutOfBounds   = errors.New("sequence number out of bounds")

	// ErrPayloadTooLarge is returned when a record is larger than the maximum payload size.
	ErrPayloadTooLarge = frame.ErrPayloadTooLarge
)

// EmptySegmentCapacity returns the size of the largest single record that fits in an empty
// segment of the given size: what is left after the segment header, the frame header
// of the record, a checkpoint when 1 record completes the stride, and the end marker.
func EmptySegmentCapacity(maxSegmentSize int64, checkpointStride uint16) int64 {
	overhead := int64(header.HeaderSize) + 2*frame.HeaderSize

	overhead += int64(checkpoint.NewTracker(checkpointStride).PredictCheckpoints(1)) * frame.HeaderSize

	return maxSegmentSize - overhead
}

// Chain is responsible for loading existing segments from disk and managing them.
type Chain struct {
	failed         error
	loader         *Loader
	list           *List
	snapshot       atomic.Pointer[Snapshot]
	maxSegmentSize int64
	maxDiskSize    int64
	diskSize       int64
	logicalSize    int64
	maxSegments    int
	mu             sync.RWMutex
}

// NewChain creates and loads a Chain using the configured loader location.
func NewChain(
	maxSegmentSize int64,
	maxDiskSize int64,
	maxSegments int,
	loader *Loader,
) (*Chain, error) {
	if loader == nil {
		return nil, errors.New("chain loader is required")
	}

	chain := &Chain{
		maxDiskSize:    maxDiskSize,
		maxSegments:    maxSegments,
		maxSegmentSize: maxSegmentSize,
		loader:         loader,
		list:           NewList(nil),
	}

	chain.mu.Lock()
	defer chain.mu.Unlock()

	// Load also returns the segments that it did load when it fails
	segments, err := chain.loader.Load()
	if err == nil {
		err = chain.adopt(segments)
	}

	if err != nil {
		for _, segment := range segments {
			err = errors.Join(err, segment.Close())
		}

		return nil, err
	}

	chain.publishSnapshot()

	return chain, nil
}

// adopt makes the loaded segments the segments of the chain, if they fit the limits.
// Caller must hold chain.mu.
func (c *Chain) adopt(segments []*Segment) error {
	list := NewList(slices.Clone(segments))
	list.Sort()

	if err := validateContiguous(list.Slice()); err != nil {
		return err
	}

	if c.maxSegments > 0 && list.Len() > c.maxSegments {
		return fmt.Errorf("too many segments: %d, max allowed: %d", list.Len(), c.maxSegments)
	}

	var diskSize, logicalSize int64

	for _, segment := range list.Slice() {
		logicalSize += segment.WriteOffset()
		diskSize += segment.DiskSize()
	}

	if c.maxDiskSize > 0 && diskSize > c.maxDiskSize {
		return fmt.Errorf(
			"loaded disk size %d exceeds MaxDiskSize %d: %w",
			diskSize,
			c.maxDiskSize,
			ErrDiskFull,
		)
	}

	c.list, c.diskSize, c.logicalSize = list, diskSize, logicalSize

	return nil
}

// DiskSize returns the accounted physical size of retained segment files.
func (c *Chain) DiskSize() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.diskSize
}

// LogicalSize returns the accounted logical size of retained segment files.
func (c *Chain) LogicalSize() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.logicalSize
}

// Segments returns a copy of the list of readable segments.
func (c *Chain) Segments() *List {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.list.Clone()
}

// Snapshot returns the segments as of the last change. It does not lock, and
// the result must not be modified.
func (c *Chain) Snapshot() *Snapshot {
	return c.snapshot.Load()
}

// publishSnapshot makes the current list of segments visible to scanners.
// Caller must hold chain.mu.
func (c *Chain) publishSnapshot() {
	c.snapshot.Store(&Snapshot{segments: slices.Clone(c.list.Slice())})
}

// Head returns the first segment managed by the log.
func (c *Chain) Head() *Segment {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.list.Head()
}

// Tail returns the last segment managed by the log.
func (c *Chain) Tail() *Segment {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.list.Tail()
}

// AppendBatch appends multipart payloads atomically with chain-level admission and accounting.
func (c *Chain) AppendBatch(batch []frame.MultiPartPayload) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(batch) == 0 {
		return 0, nil
	}

	if c.failed != nil {
		return 0, c.failed
	}

	lastSegment, err := c.admitAppendTargetLocked(batch)
	if err != nil {
		return 0, err
	}

	oldDiskSize := lastSegment.DiskSize()
	startSeq := lastSegment.NextSequence()
	n, err := lastSegment.AppendBatch(batch)
	if err != nil {
		return 0, c.checkFailed(err)
	}

	c.diskSize += lastSegment.DiskSize() - oldDiskSize
	c.logicalSize += n

	return startSeq + uint64(len(batch)) - 1, nil
}

// AppendOne appends one payload with chain-level admission and accounting.
func (c *Chain) AppendOne(payload []byte) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.failed != nil {
		return 0, c.failed
	}

	lastSegment, err := c.admitAppendTargetLocked([]frame.MultiPartPayload{{payload}})
	if err != nil {
		return 0, err
	}

	oldDiskSize := lastSegment.DiskSize()
	seq := lastSegment.NextSequence()

	n, err := lastSegment.AppendOne(payload)
	if err != nil {
		return 0, c.checkFailed(err)
	}

	c.logicalSize += n
	c.diskSize += lastSegment.DiskSize() - oldDiskSize

	return seq, nil
}

// emptySegmentSize returns the size of a new segment after appending the batch:
// the segment header, the frames, a checkpoint if the batch completes the stride,
// and the end marker.
func (c *Chain) emptySegmentSize(batch []frame.MultiPartPayload) int64 {
	size := int64(header.HeaderSize) + frame.HeaderSize

	for _, payload := range batch {
		size += frame.HeaderSize + payload.Size()
	}

	size += int64(checkpoint.NewTracker(c.loader.factory.checkpointStride).PredictCheckpoints(len(batch))) *
		frame.HeaderSize

	return size
}

// checkFailed makes a segment failure sticky for the whole chain: appends rotate
// to new segments, so refusing appends on one segment is not enough.
// Caller must hold chain.mu.
func (c *Chain) checkFailed(err error) error {
	if errors.Is(err, ErrFailed) {
		c.failed = err
	}

	return err
}

// admitAppendTargetLocked returns the segment that should receive an append of appendSize bytes.
// Caller must hold chain.mu.
func (c *Chain) admitAppendTargetLocked(batch []frame.MultiPartPayload) (*Segment, error) {
	// a batch is never split over segments, so it has to fit in an empty one
	if c.maxSegmentSize > 0 && c.emptySegmentSize(batch) > c.maxSegmentSize {
		return nil, errors.Join(ErrSegmentFull, fmt.Errorf(
			"%w: it needs %d bytes in an empty segment, which holds %d",
			ErrBatchTooLarge,
			c.emptySegmentSize(batch),
			c.maxSegmentSize,
		))
	}

	// current tail
	tail := c.list.Tail()

	// there must always be at least one segment
	if tail == nil {
		panic("chain has no tail segment")
	}

	// if the append fits in the current tail, return it
	tailAppendSize, err := tail.AppendSize(batch...)
	if err != nil {
		return nil, err
	}

	projectedLogicalSize, err := tail.ProjectedSize(batch...)
	if err != nil {
		return nil, err
	}

	if c.maxSegmentSize <= 0 || projectedLogicalSize <= c.maxSegmentSize {
		// guard against exceeding max disk size if set
		projectDiskSize := c.diskSize + tailAppendSize

		if c.maxDiskSize > 0 && projectDiskSize > c.maxDiskSize {
			return nil, ErrDiskFull
		}

		return tail, nil
	}

	// the current tail is full: check if there's room for a new segment
	if c.maxSegments <= 0 || c.list.Len() < c.maxSegments {
		// max segments allows, but creating a new segment adds a header
		// and pre-allocated bytes
		projectDiskSize := c.diskSize + c.loader.ProjectDiskSize(batch)
		if c.maxDiskSize > 0 && projectDiskSize > c.maxDiskSize {
			return nil, ErrDiskFull
		}

		var newSegment *Segment
		newSegment, err = c.loader.Create(tail.NextSequence())
		if err != nil {
			// creating a new segment failed, we cannot proceed.
			return nil, err
		}

		// add it as the new tail
		c.list.Add(newSegment)
		c.publishSnapshot()

		// update size accounting
		c.diskSize += newSegment.DiskSize()
		c.logicalSize += newSegment.WriteOffset()

		return newSegment, nil
	}

	if c.maxSegments == 1 {
		// if max segments is 1, we shouldn't try to recycle the old head, because it is the same as the tail
		return nil, ErrSegmentFull
	}

	// the number of segments is bounded and we're at the max
	// so we'll try to recycle the old head segment to use as the new tail
	oldHead := c.list.Head()

	// sanity check: the old head should not be the same as the tail
	// should be impossible to reach this point if maxSegments is not 1,
	// which is checked above
	if oldHead == tail {
		return nil, errors.New("cannot recycle head segment: head is the same as tail")
	}

	// The old head leaves the chain first, so scanners do not find a segment that is about to
	// be closed.
	c.list.Shift()
	c.publishSnapshot()

	// Closing it keeps scanners from reading while the file is recycled. When this fails
	// the handles are still released as far as possible, and recycling works on the file by
	// path, so nothing of the old segment is needed.
	_ = oldHead.Close() //nolint:errcheck

	newSegment, recycleErr := c.loader.Recycle(oldHead.Name(), tail.NextSequence())
	if recycleErr != nil {
		// recycling failed, so remove the file and create a new one instead
		if removeErr := c.loader.fs.Remove(oldHead.Name()); removeErr != nil {
			// The file is still there, and still counts. The head goes back in the chain, closed,
			// so that this is tried again, and loading does not find a segment that the chain forgot.
			c.list.Unshift(oldHead)
			c.publishSnapshot()

			return nil, errors.Join(recycleErr, removeErr)
		}
	}

	// the old file is gone or was recycled, update size accounting
	c.diskSize -= oldHead.DiskSize()
	c.logicalSize -= oldHead.WriteOffset()

	if newSegment == nil {
		newSegment, err = c.loader.Create(tail.NextSequence())
		if err != nil {
			// The old head is gone, and size accounting says so, so this can be attempted again.
			return nil, errors.Join(recycleErr, err)
		}
	}

	// add it as the new tail
	c.list.Add(newSegment)
	c.publishSnapshot()

	// update size accounting again
	c.diskSize += newSegment.DiskSize()
	c.logicalSize += newSegment.WriteOffset()

	// return the new/recycled segment as the new tail
	return newSegment, nil
}

// TruncateBefore removes all segments where the highest sequence number
// is less than the given sequence number and sets the read offset of
// of the first kept segment to match the given sequence number.
func (c *Chain) TruncateBefore(seq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishSnapshot()

	if c.failed != nil {
		return c.failed
	}

	if (c.list.Len()) == 0 {
		return nil
	}

	headStart := c.list.Head().StartSequence()
	tailNext := c.list.Tail().NextSequence()

	switch {
	// sequence is before the head, nothing to do
	case seq < headStart:
		return nil
	// sequence is after the tail, error
	case seq > tailNext:
		return ErrOutOfBounds
	// sequence is equal to the next sequence number of the tail, remove all segments
	// except the last one and set the read offset of the last segment to the write offset
	case seq == tailNext:
		s := c.list.Tail()
		if err := c.removeHeadsBefore(s); err != nil {
			return err
		}

		// set the read offset of the last segment to the write offset
		// so that it is effectively empty for reading
		if err := s.TruncateBefore(s.writeOffset, s.NextSequence()); err != nil {
			return c.checkFailed(fmt.Errorf("failed to truncate segment %s: %w", s.Name(), err))
		}

		return nil
	}

	// find the segment that contains the given sequence number
	s := c.list.Search(seq)
	if s == nil {
		return ErrOutOfBounds
	}

	if err := c.removeHeadsBefore(s); err != nil {
		return err
	}

	// if the segment's start sequence is less than the given sequence number,
	// we need to set the start sequence to the given sequence number
	if s.StartSequence() < seq {
		// find the read offset within the segment and persist it
		sc, err := s.NewScanner(0, true)
		if err != nil {
			return fmt.Errorf("failed to create scanner for segment %s: %w", s.Name(), err)
		}

		if err := sc.SeekSequence(seq); err != nil {
			return fmt.Errorf("failed to seek to sequence %d in segment %s: %w", seq, s.Name(), err)
		}

		if err := s.TruncateBefore(sc.NextOffset, sc.NextSequence()); err != nil {
			return c.checkFailed(fmt.Errorf("failed to truncate segment %s: %w", s.Name(), err))
		}
	}

	return nil
}

// removeHeadsBefore removes the segments in front of keep, and stops at the first failure.
// What is left is a chain, so it is always safe to stop.
// Caller must hold chain.mu.
func (c *Chain) removeHeadsBefore(keep *Segment) error {
	for c.list.Len() > 1 && c.list.Head() != keep {
		if err := c.removeHead(); err != nil {
			return errors.Join(err, c.syncDirectory())
		}
	}

	return c.syncDirectory()
}

// removeHead removes the first segment from the chain, and from disk.
// The file goes first: when that fails the segment is left alone, and the chain is as it was.
// Caller must hold chain.mu.
func (c *Chain) removeHead() error {
	return c.removeSegment(c.list.Head(), func() { c.list.Shift() })
}

// removeTail removes the last segment from the chain, and from disk. Segments are
// removed from the end, so what is left is always contiguous, also after a crash.
// Caller must hold chain.mu.
func (c *Chain) removeTail() error {
	return c.removeSegment(c.list.Tail(), func() { c.list.Pop() })
}

func (c *Chain) removeSegment(s *Segment, leave func()) error {
	if err := c.loader.fs.Remove(s.Name()); err != nil {
		return err
	}

	c.diskSize -= s.DiskSize()
	c.logicalSize -= s.WriteOffset()

	// scanners must not find a segment that is about to be closed
	leave()
	c.publishSnapshot()

	return s.Close()
}

// syncDirectory makes the removal of segment files durable.
func (c *Chain) syncDirectory() error {
	return c.loader.fs.SyncDir(c.loader.directory)
}

// TruncateAfter finds the segment that contains the given sequence number,
// removes all segments after it, and truncates that segment to the given sequence
// number if needed.
func (c *Chain) TruncateAfter(seq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.publishSnapshot()

	if c.failed != nil {
		return c.failed
	}

	// find the segment that contains the given sequence number
	keep := c.list.Search(seq)
	if keep == nil {
		return ErrOutOfBounds
	}

	for c.list.Tail() != keep {
		if err := c.removeTail(); err != nil {
			return errors.Join(err, c.syncDirectory())
		}
	}

	if err := c.syncDirectory(); err != nil {
		return err
	}

	oldLogicalSize := keep.WriteOffset()
	oldDiskSize := keep.DiskSize()

	if err := keep.TruncateAfter(seq + 1); err != nil {
		return c.checkFailed(err)
	}

	c.diskSize -= oldDiskSize - keep.DiskSize()
	c.logicalSize -= oldLogicalSize - keep.WriteOffset()

	if keep.startOffset > keep.writeOffset {
		if err := keep.TruncateBefore(keep.writeOffset, keep.NextSequence()); err != nil {
			return c.checkFailed(err)
		}
	}

	return nil
}

// Sync flushes file contents and metadata of all active segments to stable storage.
func (c *Chain) Sync() error {
	for _, seg := range c.Segments().Slice() {
		if err := seg.Sync(); err != nil {
			return err
		}
	}

	return nil
}

// Close closes all segments in the log and returns any errors that occurred during closing.
func (c *Chain) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var err error
	for _, segment := range c.list.Slice() {
		err = errors.Join(err, segment.Close())
	}

	return err
}
