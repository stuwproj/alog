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

// RangeFrom iterates records in [startSeq, endSeq), or to the end of the log when
// endSeq is 0, or beyond what the log holds. The end is the end of the log when the
// call starts: records that are appended while it runs are not visited.
// It can be called while the log is appended to.
func RangeFrom(
	l *Log,
	startSeq uint64,
	endSeq uint64,
	fn func(seq uint64, payload []byte) error,
) error {
	first, next := l.readable()

	startSeq = max(startSeq, first)
	if endSeq == 0 || endSeq > next {
		endSeq = next
	}

	if startSeq >= endSeq {
		return nil
	}

	s, err := NewScanner(l)
	if err != nil {
		return err
	}

	if seekErr := s.SeekSequence(startSeq); seekErr != nil {
		return seekErr
	}

	s.StopAt(endSeq)

	for s.Scan() {
		fnErr := fn(s.LastSequence(), s.Data())
		if fnErr != nil {
			return fnErr
		}
	}

	return s.Err()
}
