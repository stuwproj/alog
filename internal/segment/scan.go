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
	"io"

	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/scanner"
)

// NewScannerWithValidator creates a scanner for this segment with a custom validator.
// If the validator is nil, a default validator will be used that checks for EOF markers
// and generation mismatches.
//
// The scanner only reads up to the published offset, and reaching it ends the scan
// with io.EOF. Extend moves the end of a scanner forward when more was published.
func (s *Segment) NewScannerWithValidator(
	limit uint32,
	v scanner.Validator,
	unsafe bool,
) (*scanner.Scanner, error) {
	return s.newScanner(-1, limit, v, unsafe, true)
}

// newScannerAt creates a scanner starting at the given physical offset,
// which must be the start of a frame. It reads the whole mapping, including
// the end marker, which is what recovery needs, and borrows the payloads from it.
func (s *Segment) newScannerAt(
	offset int64,
	limit uint32,
	v scanner.Validator,
) (*scanner.Scanner, error) {
	return s.newScanner(offset, limit, v, true, false)
}

// newScanner creates a scanner. A bounded scanner is a reader of the segment: it uses what the
// segment published, so it can be created while the segment is appended to or truncated. The
// start sequence and the read offset go before the index, because a truncation of the head
// resets the index before it publishes them: a start that is new then goes with an index that
// is empty, and an old start with an index that fits it. The offset is where scanning starts,
// -1 for the read offset of the segment.
func (s *Segment) newScanner(
	offset int64,
	limit uint32,
	v scanner.Validator,
	unsafe bool,
	bounded bool,
) (*scanner.Scanner, error) {
	if v == nil {
		v = scanner.NewPreallocFrameValidator(s.frameGen)
	}

	region, err := s.mapping()
	if err != nil {
		return nil, err
	}

	data := region.Bytes()

	var baseSeq uint64

	if bounded {
		position := s.pubRead.Load()
		baseSeq = position.start

		if offset < 0 {
			offset = max(position.offset, int64(header.HeaderSize))
		}
	} else {
		baseSeq = s.meta.StartSequence()
	}

	index := s.offsetIndex.Snapshot()

	if bounded {
		data = data[:min(int64(len(data)), s.Published())]
	}

	sc := &scanner.Scanner{
		BaseSeq: baseSeq,
		FrameScanner: &scanner.FrameScanner{
			Loader:     &scanner.SliceLoader{Copy: !unsafe, Data: data},
			Validator:  v,
			Index:      index,
			NextIndex:  0,
			NextOffset: offset,
			Limit:      limit,
		},
	}

	return sc, sc.Init()
}

// Refresh sets the end of a bounded scanner to the published offset. The end
// moves forward when more was appended, and backward when the segment was
// truncated, so the scanner never reads what was not published or is being
// replaced. It reports whether the end moved forward, in which case the end of
// data state of the scanner is cleared, and there may be more to read.
// The scanner must have been created by this segment, and the caller must hold the lease.
func (s *Segment) Refresh(sc *scanner.Scanner) bool {
	loader, ok := sc.Loader.(*scanner.SliceLoader)
	if !ok {
		return false
	}

	region := s.currentMapping()
	if region == nil {
		return false
	}

	data := region.Bytes()
	end := min(int64(len(data)), s.Published())
	current := int64(len(loader.Data))

	if end == current {
		return false
	}

	loader.Data = data[:end]

	if end < current {
		return false
	}

	if errors.Is(sc.Err(), io.EOF) {
		return sc.Seek(sc.NextIndex, sc.NextOffset) == nil
	}

	return true
}

// NewLimitedScanner creates a scanner for this segment that reads at most limit
// bytes of every payload. Payloads are not checksum-verified, because that needs all
// of the bytes. The frame headers are validated, through the checkpoints, when the
// segment is loaded.
func (s *Segment) NewLimitedScanner(limit uint32) (*scanner.Scanner, error) {
	return s.NewScannerWithValidator(limit, scanner.NewPreallocFrameValidator(s.frameGen), false)
}

// NewScanner creates a scanner for this segment.
func (s *Segment) NewScanner(limit uint32, unsafe bool) (*scanner.Scanner, error) {
	return s.NewScannerWithValidator(limit, scanner.NewChecksumValidator(s.frameGen), unsafe)
}

// mapping returns the mapping of the file, and maps it first if that was not done yet.
// The caller must hold the lease, which keeps the mapping from being released.
func (s *Segment) mapping() (*scanner.MMapRegion, error) {
	s.mmapMu.Lock()
	defer s.mmapMu.Unlock()

	if s.mmap != nil {
		return s.mmap, nil
	}

	mmap, err := scanner.OpenMMap(s.file)
	if err != nil {
		return nil, err
	}

	s.mmap = mmap

	return mmap, nil
}

// currentMapping returns the mapping of the file if there is one.
func (s *Segment) currentMapping() *scanner.MMapRegion {
	s.mmapMu.Lock()
	defer s.mmapMu.Unlock()

	return s.mmap
}
