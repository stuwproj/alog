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
	"strconv"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/storage"
	modframe "stuw.net/frame"
)

// position is a frame boundary in a segment together with the logical index
// and checkpoint tracker state at that boundary.
type position struct {
	state  checkpoint.State
	offset int64
	index  uint32
}

// initValidator replays frames into a checkpoint tracker. In verify mode it
// also checks the payload checksum of every data frame, which is used to
// recover the tail of a segment when the checkpoint chain cannot be validated.
type initValidator struct {
	tracker *checkpoint.Tracker
	gen     uint16
	verify  bool
	sawEOF  bool
}

func (v *initValidator) Validate(h *frame.Header, p []byte) error {
	if err := h.CheckFlags(); err != nil {
		return err
	}

	if h.Generation() != v.gen {
		return io.ErrUnexpectedEOF
	}

	if v.verify && !h.IsCheckpoint() {
		if p == nil || h.Checksum() != modframe.Checksum(h, p, 0) {
			return frame.ErrInvalidChecksum
		}
	}

	// EOF frames are checkpoints and must be validated before ending the scan.
	if err := v.tracker.Validate(h, nil); err != nil {
		return err
	}

	if h.IsEOF() {
		v.sawEOF = true
		return io.EOF
	}

	return nil
}

// init scans the segment to restore the write position, the offset index,
// and the checkpoint tracker.
//
// The headers are validated against the checkpoint chain, which must end in a
// valid EOF checkpoint. If it does not, the writer was interrupted. Every
// checkpoint is a batch boundary, so the frames following the last valid
// checkpoint are verified one by one using their payload checksums, and the
// segment is cut after the last complete batch. A batch that was not confirmed
// to the caller is never kept partially.
func (s *Segment) init() error {
	s.offsetIndex.Reset()

	start := position{
		offset: max(s.startOffset, int64(header.HeaderSize)),
		state:  s.bootstrapState(),
	}

	end, complete, err := s.scanFrom(start, false)
	if err != nil {
		return err
	}

	if !complete {
		if end, complete, err = s.scanFrom(end, true); err != nil {
			return err
		}
	}

	if !complete {
		if s.logger != nil {
			s.logger("recovering segment tail at offset " + strconv.FormatInt(end.offset, 10))
		}

		if err = s.writeEOF(end); err != nil {
			return fmt.Errorf("failed to recover segment tail: %w", err)
		}
	}

	s.nextWriteIndex = end.index
	s.writeOffset = end.offset
	s.publish()
	s.tracker.SetState(end.state.CRC, end.state.Remaining)

	return nil
}

// scanFrom scans frames starting at pos and reports whether a valid EOF
// checkpoint was reached. If so, the returned position is that of the EOF frame.
// Otherwise it is the last position that can be trusted: the last validated
// checkpoint, or in verify mode the end of the last verified batch.
func (s *Segment) scanFrom(pos position, verify bool) (position, bool, error) {
	tracker := checkpoint.NewTracker(s.tracker.Stride())
	tracker.SetState(pos.state.CRC, pos.state.Remaining)

	v := &initValidator{tracker: tracker, gen: s.meta.FrameGeneration(), verify: verify}

	limit := uint32(modframe.HeadersOnly)
	if verify {
		limit = modframe.MaxPayloadSize
	}

	sc, err := s.newScannerAt(pos.offset, limit, v)
	if err != nil {
		return pos, false, fmt.Errorf("failed to create scanner for init: %w", err)
	}

	trusted := pos
	current := pos

	for sc.Scan() {
		current.offset = sc.NextOffset
		current.state = tracker.State()

		if sc.Header.IsCheckpoint() {
			if s.logger != nil {
				s.logger(
					"checkpoint frame for index " +
						strconv.Itoa(int(current.index)) + ", offset " +
						strconv.FormatInt(sc.LastOffset, 10),
				)
			}

			s.offsetIndex.Add(current.index, sc.LastOffset)
			trusted = current

			continue
		}

		current.index += uint32(sc.Header.IndexStride())

		// only the end of a batch is a point where the segment can be cut
		if verify && sc.Header.IsBatchEnd() {
			trusted = current
		}
	}

	if v.sawEOF {
		return current, true, nil
	}

	if scanErr := sc.Err(); scanErr != nil && !errors.Is(scanErr, io.EOF) && s.logger != nil {
		s.logger("init scan stopped at offset " + strconv.FormatInt(sc.LastOffset, 10) + ": " + scanErr.Error())
	}

	return trusted, false, nil
}

// bootstrapState returns the checkpoint tracker state at the read start offset.
func (s *Segment) bootstrapState() checkpoint.State {
	if s.meta.Flags()&header.FlagCheckpointStateInitialized != 0 {
		return checkpoint.State{CRC: s.meta.CheckpointCRC(), Remaining: s.meta.CheckpointRemaining()}
	}

	return checkpoint.State{Remaining: s.tracker.Stride()}
}

// writeEOF writes an EOF checkpoint at pos and syncs it.
// In read-only mode the segment is only truncated in memory.
func (s *Segment) writeEOF(pos position) error {
	eof := checkpoint.NewEOF(s.meta.FrameGeneration(), s.tracker.Stride(), pos.state)

	if _, err := s.file.WriteAt(eof[:], pos.offset); err != nil {
		if errors.Is(err, storage.ErrReadOnly) {
			return nil
		}

		return err
	}

	if err := s.file.Sync(); err != nil {
		if errors.Is(err, storage.ErrReadOnly) {
			return nil
		}

		return fmt.Errorf("failed to sync segment file: %w", err)
	}

	return nil
}
