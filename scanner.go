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

package alog

import (
	"errors"
	"io"

	"stuw.net/alog/internal/scanner"
	"stuw.net/alog/internal/segment"
)

// ErrSegmentReaped is returned when a segment has been reaped and is no longer available for scanning.
var ErrSegmentReaped = errors.New("segment reaped")

// ErrOutOfBounds is returned when a requested position is outside retained bounds.
var ErrOutOfBounds = segment.ErrOutOfBounds

// ErrClosed is returned when scanner operations are attempted after Close.
var ErrClosed = errors.New("scanner closed")

// Scanner scans retained records across log segments.
//
// A scanner follows the log: when it has read everything there is, Scan returns
// false and Err returns nil, and a later call to Scan returns what was appended
// in the meantime, also when this is in a new segment.
//
// Payloads returned by Data are copies and remain valid after the
// underlying segment is reaped or recycled. A scanner may be used
// concurrently with Log mutations; if a segment is reaped while it is being
// scanned, or the records it read were truncated, Scan returns false and Err
// returns ErrSegmentReaped.
// A Scanner itself is not safe for concurrent use.
type Scanner struct {
	err     error
	log     *Log
	segment *segment.Segment
	sc      *scanner.Scanner
	token   segment.LeaseToken
	endSeq  uint64
	limit   uint32
	limited bool
	closed  bool
}

// NewScanner creates a scanner for the given log. It reads every payload
// completely, and verifies its checksum.
func NewScanner(l *Log) (*Scanner, error) {
	return newScanner(l, scanner.NoLimit, false)
}

// NewLimitedScanner creates a scanner that reads at most limit bytes of every
// payload. A limit of 0 reads the frame headers only. This is meant for payloads that
// start with a header of their own that the reader validates independently.
//
// The payloads are not checksum-verified, because that needs all of the bytes.
func NewLimitedScanner(l *Log, limit uint32) (*Scanner, error) {
	return newScanner(l, limit, true)
}

// attempts is how often something that races with retention is tried again.
const attempts = 5

func newScanner(l *Log, limit uint32, limited bool) (*Scanner, error) {
	var (
		seg   *segment.Segment
		sc    *scanner.Scanner
		token segment.LeaseToken
		err   error
	)

	// the head can be removed between taking the snapshot and reading it
	for range attempts {
		seg = l.chain.Snapshot().Head()

		if sc, token, err = newSegmentScanner(seg, limit, limited); !errors.Is(err, ErrSegmentReaped) {
			break
		}
	}

	if err != nil {
		return nil, err
	}

	return &Scanner{
		log:     l,
		segment: seg,
		sc:      sc,
		token:   token,
		limit:   limit,
		limited: limited,
	}, nil
}

// Err returns the error that occurred during scanning, if any.
func (s *Scanner) Err() error {
	if s.err != nil {
		if errors.Is(s.err, io.EOF) {
			return nil
		}

		return s.err
	}

	err := s.sc.Err()
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

// Scan advances to the next retained record across segments. It returns false
// when there is no more to read, and Err returns nil, or when it failed.
// At the end of the log a later call can return true, once records were appended.
func (s *Scanner) Scan() bool {
	if s.closed || s.err != nil {
		return false
	}

	for {
		ok, err := s.scanSegment()
		if err != nil {
			s.setErr(err)
			return false
		}

		if ok {
			// checkpoints are part of the segment layout, they are not records
			if s.sc.Header.IsCheckpoint() {
				continue
			}

			// the end is exclusive, and the scan is over once it is reached
			if s.endSeq > 0 && s.sc.LastSequence() >= s.endSeq {
				s.setErr(io.EOF)

				return false
			}

			return true
		}

		// io.EOF is the end of what was readable, anything else is a failure
		if err = s.sc.Err(); err != nil && !errors.Is(err, io.EOF) {
			s.setErr(err)
			return false
		}

		if !s.nextSegment() {
			return false
		}
	}
}

// nextSegment moves to the segment that follows the current one in the chain.
// It returns false when the scanner has to stop: it is at the end of the log, which
// is not final, or it failed.
func (s *Scanner) nextSegment() bool {
	snapshot := s.log.chain.Snapshot()

	idx := snapshot.IndexOf(s.segment)
	if idx < 0 {
		s.setErr(ErrSegmentReaped)
		return false
	}

	if idx == snapshot.Len()-1 {
		return false
	}

	// The segment is not the tail any more, so nothing is appended to it. Read
	// what was published after the scanner last looked, before moving on.
	lease := s.segment.Lease()
	if !lease.AcquireAt(&s.token, s.sc.NextSequence()) {
		s.setErr(ErrSegmentReaped)
		return false
	}

	more := s.segment.Refresh(s.sc)
	lease.Release()

	if more {
		return true
	}

	next := snapshot.At(idx + 1)

	sc, token, err := newSegmentScanner(next, s.limit, s.limited)
	if err != nil {
		s.setErr(err)
		return false
	}

	s.segment = next
	s.sc = sc
	s.token = token

	return true
}

// Close marks the scanner as closed and prevents further scanning.
// Closing is idempotent. If the scanner already reached a terminal state
// (end of log or an error), Close does not change what Err reports.
func (s *Scanner) Close() error {
	if s.closed {
		return nil
	}

	s.closed = true

	// a scanner that has read everything is not failed by closing it
	if s.err == nil && !errors.Is(s.sc.Err(), io.EOF) {
		s.setErr(ErrClosed)
	}

	return nil
}

// StopAt sets an exclusive end sequence bound for scanning.
func (s *Scanner) StopAt(endSeq uint64) {
	s.endSeq = endSeq
}

// Data returns the payload of the current frame.
func (s *Scanner) Data() []byte {
	if s.sc == nil || s.sc.Frame == nil {
		return nil
	}

	return s.sc.Frame.Payload
}

// SeekSequence positions the scanner so the next Scan starts at seq. A sequence
// before the first retained record positions it at the first record. For a
// sequence at or after the next sequence of the log, ErrOutOfBounds is returned,
// and the scanner stays where it was. Seeking after the scan was stopped by
// StopAt starts a new scan.
func (s *Scanner) SeekSequence(seq uint64) error {
	if errors.Is(s.err, io.EOF) && !s.closed {
		s.err = nil
	}

	if s.closed || s.err != nil {
		return s.Err()
	}

	var err error

	// Retention can move the start of the log while the segment is looked up.
	// A sequence that is not retained any more starts at the first record, so try again.
	for range attempts {
		var retry bool

		if retry, err = s.seek(seq); !retry {
			break
		}
	}

	if err != nil && !errors.Is(err, ErrOutOfBounds) {
		s.setErr(err)
	}

	return err
}

// seek positions the scanner at seq. It reports whether the log changed in a way that
// makes it worth trying again.
func (s *Scanner) seek(seq uint64) (retry bool, err error) {
	snapshot := s.log.chain.Snapshot()
	if head := snapshot.Head(); head != nil {
		seq = max(seq, head.PublishedStart())
	}

	seg := snapshot.Locate(seq)
	if seg == nil {
		return false, ErrOutOfBounds
	}

	sc, token, err := newSegmentScanner(seg, s.limit, s.limited)
	if err != nil {
		return errors.Is(err, ErrSegmentReaped), err
	}

	lease := seg.Lease()
	if !lease.AcquireAt(&token, seq) {
		return true, ErrSegmentReaped
	}

	seg.Refresh(sc)

	err = sc.SeekSequence(seq)
	lease.Release()

	switch {
	case errors.Is(err, scanner.ErrInvalidSequence):
		// the start of the segment moved past seq
		return true, ErrOutOfBounds
	case errors.Is(err, io.EOF):
		return false, ErrOutOfBounds
	case err != nil:
		return false, err
	}

	s.segment = seg
	s.sc = sc
	s.token = token

	return false, nil
}

// LastSequence returns the sequence number of the current frame.
func (s *Scanner) LastSequence() uint64 {
	return s.sc.LastSequence()
}

// NextSequence returns the sequence number that the next Scan call will attempt to read.
func (s *Scanner) NextSequence() uint64 {
	return s.sc.NextSequence()
}

// LastOffset returns the physical offset of the current frame.
func (s *Scanner) LastOffset() int64 {
	return s.sc.LastOffset
}

// NextOffset returns the physical offset where the next frame starts.
func (s *Scanner) NextOffset() int64 {
	return s.sc.NextOffset
}

// scanSegment advances the segment scanner while holding the segment's read lease.
// Payloads are copied out of the mapping, so the lease is only held for the
// duration of the read.
func (s *Scanner) scanSegment() (bool, error) {
	lease := s.segment.Lease()
	if !lease.AcquireAt(&s.token, s.sc.NextSequence()) {
		return false, ErrSegmentReaped
	}

	s.segment.Refresh(s.sc)

	ok := s.sc.Scan()
	for !ok && errors.Is(s.sc.Err(), io.EOF) && s.segment.Refresh(s.sc) {
		ok = s.sc.Scan()
	}

	lease.Release()

	return ok, nil
}

// newSegmentScanner creates a copying segment scanner while holding the
// segment's read lease, so the mapping cannot be released during creation.
func newSegmentScanner(
	seg *segment.Segment,
	limit uint32,
	limited bool,
) (*scanner.Scanner, segment.LeaseToken, error) {
	lease := seg.Lease()
	if !lease.Acquire() {
		return nil, segment.LeaseToken{}, ErrSegmentReaped
	}
	defer lease.Release()

	token := lease.NewToken()

	if limited {
		sc, err := seg.NewLimitedScanner(limit)

		return sc, token, err
	}

	sc, err := seg.NewScanner(limit, false)

	return sc, token, err
}

func (s *Scanner) setErr(err error) {
	if err == nil || s.err != nil {
		return
	}

	s.err = err
}
