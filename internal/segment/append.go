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
	"errors"
	"fmt"
	"io"
	"math"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
)

// ErrFailed is returned by appends after a failed write could not be rolled back.
//
// The state of the segment file is then unknown, so the segment refuses further
// appends. Reopening the log recovers it.
var ErrFailed = errors.New("segment failed: reopen the log to recover")

// writeError marks an append error that occurred while writing or syncing,
// which means the segment file may have been modified.
type writeError struct{ err error }

func (e *writeError) Error() string { return e.err.Error() }
func (e *writeError) Unwrap() error { return e.err }

const (
	// iovMax is the maximum number of buffers that can be written in a single writev call.
	//
	// This is 1024 on Linux and macOS, we use it to prevent retrying a writev call that
	// would exceed the system limit. The actual limit can be queried with `getconf IOV_MAX`.
	iovMax = 1024
)

// AppendSize returns the number of bytes that appending the multipart payloads as
// one batch writes at the write offset: the frames, a checkpoint if the batch
// completes the stride, and the end marker.
func (s *Segment) AppendSize(batch ...frame.MultiPartPayload) (int64, error) {
	size, _, _, err := s.batchSize(batch)
	return int64(size), err
}

// AppendBatch appends multipart payload records to the segment as one batch.
//
// The batch is all or nothing: the last frame carries FlagBatchEnd, and a
// checkpoint is only written after a complete batch, so that recovery can
// always cut the segment at a batch boundary.
//
// This should not be called concurrently with other append operations on the same segment.
//
// When writing or syncing fails, the in-memory state is rolled back and the end
// marker that the batch overwrote is restored, so that the next append continues
// at the same offset. If the end marker cannot be restored the segment is failed,
// and returns ErrFailed for every following append.
func (s *Segment) AppendBatch(batch []frame.MultiPartPayload) (written int64, err error) {
	if s.failed != nil {
		return 0, s.failed
	}

	if len(batch) == 0 {
		return 0, nil
	}

	saved := s.tracker.State()

	frameIdx, written, err := s.append(
		s.nextWriteIndex,
		s.writeOffset,
		batch,
	)
	if err != nil {
		return 0, s.rollbackAppend(saved, err)
	}

	s.nextWriteIndex = frameIdx
	s.writeOffset += written
	s.publish()

	s.indexPendingCheckpoint()

	return written, nil
}

// indexPendingCheckpoint adds the checkpoint of the batch that was just written to the index.
// This is done once the batch is published: a scanner can then only get an entry for a
// checkpoint that is readable, and an entry that is not there yet only makes a seek slower.
func (s *Segment) indexPendingCheckpoint() {
	if s.cpPending {
		s.offsetIndex.Add(s.cpIndex, s.cpOffset)
		s.cpPending = false
	}
}

// rollbackAppend undoes the in-memory effects of a failed append, and restores
// the end marker on disk if the file may have been written to.
func (s *Segment) rollbackAppend(saved checkpoint.State, appendErr error) error {
	s.tracker.SetState(saved.CRC, saved.Remaining)
	s.cpPending = false

	if _, ok := errors.AsType[*writeError](appendErr); !ok {
		// failed before anything was written
		return appendErr
	}

	// The batch overwrote the end marker at writeOffset, and part of it may have
	// reached the file. Put the end marker back, so whatever is behind it is never
	// read, and make that durable.
	eof := checkpoint.NewEOF(s.meta.FrameGeneration(), s.tracker.Stride(), saved)

	if _, err := s.file.WriteAt(eof[:], s.writeOffset); err != nil {
		return s.fail(appendErr, err)
	}

	if err := s.file.Sync(); err != nil {
		return s.fail(appendErr, err)
	}

	return appendErr
}

func (s *Segment) fail(appendErr, restoreErr error) error {
	s.failed = fmt.Errorf(
		"%w: restoring the end of the segment failed: %w (append error: %w)",
		ErrFailed,
		restoreErr,
		appendErr,
	)

	return s.failed
}

// AppendOne appends a single payload record to the segment.
func (s *Segment) AppendOne(payload []byte) (written int64, err error) {
	return s.AppendBatch([]frame.MultiPartPayload{{payload}})
}

func (s *Segment) append(
	idxStart uint32,
	writeOffset int64,
	batch []frame.MultiPartPayload,
) (frameIdx uint32, lN int64, err error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}

	physicalSize, vectors, hdrCount, err := s.batchSize(batch)
	if err != nil {
		return 0, 0, err
	}

	parts := 0
	for _, payload := range batch {
		parts += len(payload)
	}

	if len(s.hdrs) < hdrCount {
		s.hdrs = make([]frame.Header, hdrCount)
	}

	if vectors != hdrCount+parts {
		panic(fmt.Sprintf(
			"batchSize returned inconsistent vector count: %d != %d + %d",
			vectors,
			hdrCount,
			parts,
		))
	}

	if len(s.bufs) < vectors {
		s.bufs = make([][]byte, vectors)
	}

	var bufIdx, hIdx int
	frameIdx = idxStart
	logicalSize := int64(0)

	for i, payload := range batch {
		s.hdrs[hIdx] = frame.Header{}

		if i == len(batch)-1 {
			s.hdrs[hIdx].SetBatchEnd(s.meta.FrameGeneration(), 1, payload)
		} else {
			s.hdrs[hIdx].Set(s.meta.FrameGeneration(), 1, payload)
		}

		s.bufs[bufIdx] = s.hdrs[hIdx][:]
		bufIdx++

		for _, part := range payload {
			s.bufs[bufIdx] = part
			bufIdx++
		}

		logicalSize += int64(frame.HeaderSize) + payload.Size()
		frameIdx++

		s.tracker.Track(&s.hdrs[hIdx])
		hIdx++
	}

	// Checkpoints are only written at batch boundaries, so that every checkpoint
	// is a point where recovery can safely cut the segment.
	if s.tracker.ShouldCheckpoint() {
		s.tracker.Checkpoint(s.meta.FrameGeneration(), &s.hdrs[hIdx])
		s.bufs[bufIdx] = s.hdrs[hIdx][:]
		bufIdx++
		hIdx++

		// added to the index once the batch is published
		s.cpPending, s.cpIndex, s.cpOffset = true, frameIdx, writeOffset+logicalSize
		logicalSize += frame.HeaderSize
	}

	// add EOF marker
	s.tracker.EOF(s.meta.FrameGeneration(), &s.hdrs[hIdx])
	s.bufs[bufIdx] = s.hdrs[hIdx][:]
	bufIdx++

	n, err := s.persist(writeOffset, bufIdx)

	// do not keep the payloads of the caller alive until the next append
	clear(s.bufs[:bufIdx])

	if err != nil {
		return 0, 0, &writeError{err: err}
	}

	if n != int(physicalSize) {
		// TODO: retry short writes by rebuilding the original write buffers
		// and rewriting the from the same starting offset.
		return 0, 0, &writeError{
			err: fmt.Errorf("wrote %d bytes, expected %d bytes: %w", n, physicalSize, io.ErrShortWrite),
		}
	}

	return frameIdx, logicalSize, nil
}

func (s *Segment) batchSize(batch []frame.MultiPartPayload) (
	size uint32,
	vCount int,
	hCount int,
	err error,
) {
	physicalSize := 0
	vectorLength := 0

	batchCount := len(batch)
	if batchCount > math.MaxUint16 {
		return 0, 0, 0, fmt.Errorf("%w: %d records, at most %d", frame.ErrBatchTooLarge, batchCount, math.MaxUint16)
	}

	for _, payload := range batch {
		for _, part := range payload {
			physicalSize += len(part)
		}

		// +1 for each part
		vectorLength += len(payload)

		physicalSize += frame.HeaderSize
		hCount++
		vectorLength++
	}

	checkpointFrames := s.tracker.PredictCheckpoints(batchCount)

	if checkpointFrames > 0 {
		physicalSize += checkpointFrames * frame.HeaderSize
		hCount += checkpointFrames
		vectorLength += checkpointFrames
	}

	// eof marker
	physicalSize += frame.HeaderSize
	hCount++
	vectorLength++

	if physicalSize > math.MaxUint32 {
		return 0, 0, 0, fmt.Errorf(
			"%w: the batch is %d bytes, at most %d",
			frame.ErrPayloadTooLarge,
			physicalSize,
			uint32(math.MaxUint32),
		)
	}

	if vectorLength > iovMax {
		// TODO: if this happens we should split the batch into multiple
		// writev calls instead of failing
		return 0, 0, 0,
			fmt.Errorf(
				"batch exceeds IOV_MAX: %d > %d: %w",
				vectorLength,
				iovMax,
				frame.ErrBatchTooLarge,
			)
	}

	return uint32(physicalSize),
		vectorLength,
		hCount,
		nil
}

func (s *Segment) persist(writeOffset int64, bufIdx int) (int, error) {
	n, err := s.file.WritePartsAt(s.bufs[:bufIdx], writeOffset)
	if err != nil {
		return 0, err
	}

	if s.syncOnAppend {
		if err = s.file.Sync(); err != nil {
			return 0, fmt.Errorf("failed to sync segment file after append: %w", err)
		}
	}

	return n, err
}
