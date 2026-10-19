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

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
)

// TruncateAfter truncates the segment so that seq becomes the next sequence.
// Records with a sequence number >= seq are removed by writing an EOF checkpoint
// at the start of the frame for seq.
func (s *Segment) TruncateAfter(seq uint64) error {
	if s.failed != nil {
		return s.failed
	}

	if seq >= s.NextSequence() {
		return nil
	}

	sc, err := s.NewScanner(0, true)
	if err != nil {
		return err
	}

	if err = sc.SeekSequence(seq); err != nil {
		return err
	}

	return s.truncateTo(sc.NextOffset, sc.NextIndex)
}

// failTruncate makes a failed truncation permanent for the segment. The cut may have reached
// the file, wholly or in part, while the segment still counts what was beyond it. An append
// would then put records behind an end marker, and loading drops these. Appends and
// truncations return ErrFailed, until the log is opened again, which recovers the segment.
// Scanners stay below the cut.
func (s *Segment) failTruncate(err error) error {
	s.failed = fmt.Errorf("%w: truncating the segment failed: %w", ErrFailed, err)

	return s.failed
}

// truncateTo writes an EOF checkpoint at offset, which must be a frame boundary
// at or before the current write offset, and resets the write position to it.
//
// The end of the log must be a batch boundary, or recovery would cut the
// segment back to before the batch that was cut in two. When the cut is inside
// a batch, a checkpoint is written first. Checkpoints are batch boundaries.
func (s *Segment) truncateTo(offset int64, nextWriteIndex uint32) error {
	state, atBoundary, err := s.stateAt(offset)
	if err != nil {
		return err
	}

	gen := s.meta.FrameGeneration()
	stride := s.tracker.Stride()
	end := offset

	var buf []byte

	if atBoundary {
		eof := checkpoint.NewEOF(gen, stride, state)
		buf = eof[:]
	} else {
		var cp frame.Header
		cp.SetCheckpoint(gen, stride, state.CRC, 0)

		state = checkpoint.State{Remaining: stride}
		eof := checkpoint.NewEOF(gen, stride, state)

		buf = append(cp[:], eof[:]...)
		end += frame.HeaderSize
	}

	// The cut overwrites frames that scanners may be reading. No scanner reads
	// while it is written, and from then on scanners stop at the cut. A scanner
	// that already read beyond it is rejected when it continues.
	err = s.lease.Truncate(s.meta.StartSequence()+uint64(nextWriteIndex), func() error {
		s.published.Store(min(s.published.Load(), offset))
		s.pubNext.Store(min(s.pubNext.Load(), s.meta.StartSequence()+uint64(nextWriteIndex)))

		// the entries beyond the cut go before scanners can create their snapshots again
		s.offsetIndex.TruncateAfter(nextWriteIndex)

		if !atBoundary {
			s.offsetIndex.Add(nextWriteIndex, offset)
		}

		_, writeErr := s.file.WriteAt(buf, offset)

		return writeErr
	})
	if err != nil {
		return s.failTruncate(err)
	}

	if err = s.file.Sync(); err != nil {
		return s.failTruncate(fmt.Errorf("failed to sync segment file after truncation: %w", err))
	}

	s.nextWriteIndex = nextWriteIndex
	s.writeOffset = end
	s.tracker.SetState(state.CRC, state.Remaining)

	if s.startOffset > s.writeOffset {
		s.startOffset = s.writeOffset
	}

	s.publish()

	return nil
}

// TruncateBefore moves the start of the segment to offset, which becomes sequence seq,
// and persists it together with the checkpoint state at that offset.
func (s *Segment) TruncateBefore(offset int64, seq uint64) error {
	if s.failed != nil {
		return s.failed
	}

	oldNextSeq := s.NextSequence()

	if offset < int64(header.HeaderSize) || offset > s.writeOffset {
		return header.ErrInvalidSegmentHeader
	}

	if seq > s.NextSequence() {
		return header.ErrInvalidSegmentHeader
	}

	if seq < s.StartSequence() {
		return header.ErrInvalidSegmentHeader
	}

	delta := oldNextSeq - seq
	if delta > uint64(^uint32(0)) {
		return header.ErrInvalidSegmentHeader
	}

	cpState, _, err := s.stateAt(offset)
	if err != nil {
		return err
	}

	if err := header.MoveReadPosition(s.file, header.ReadPosition{
		Seq:         seq,
		ReadOffset:  offset,
		CPRemaining: cpState.Remaining,
		CPCRC:       cpState.CRC,
	}, s.meta); err != nil {
		return err
	}

	s.startOffset = offset

	// Only the head moves. The tracker describes the tail, where the next append
	// continues, so it must not change. The state at the new head is persisted
	// above, for the next load.
	s.nextWriteIndex = uint32(delta)
	s.offsetIndex.Reset()
	s.publish()

	return nil
}

// stateAt returns the checkpoint tracker state at the frame boundary target,
// i.e. the state after replaying all frames before target since the last
// checkpoint, and whether target is a batch boundary. target must not be
// beyond the write offset.
func (s *Segment) stateAt(target int64) (state checkpoint.State, atBoundary bool, err error) {
	offset := max(s.startOffset, int64(header.HeaderSize))
	state = s.bootstrapState()

	// start right after the last checkpoint before target, if indexed
	if _, cpOffset, found := s.offsetIndex.ForOffset(target - 1); found && cpOffset >= offset {
		offset = cpOffset + frame.HeaderSize
		state = checkpoint.State{Remaining: s.tracker.Stride()}
	}

	// the start of the replay is either a checkpoint or the start of the log
	if offset >= target {
		return state, true, nil
	}

	t := checkpoint.NewTracker(s.tracker.Stride())
	t.SetState(state.CRC, state.Remaining)

	sc, err := s.newScannerAt(offset, 0, nil)
	if err != nil {
		return state, false, err
	}

	atBoundary = true

	for sc.Scan() {
		if sc.LastOffset >= target {
			break
		}

		h := sc.Header
		if h.IsCheckpoint() {
			t.Reset()
			atBoundary = true

			continue
		}

		t.Track(&h)
		atBoundary = h.IsBatchEnd()
	}

	if err = sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return state, false, err
	}

	return t.State(), atBoundary, nil
}
