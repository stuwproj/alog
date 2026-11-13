// MIT License
//
// # Copyright (C) 2025 John Kleijn
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
package alog_test

import (
	"errors"
	"fmt"
	"sort"

	"github.com/cucumber/godog"

	"stuw.net/alog"
)

// spot is where a record was written.
type spot struct {
	payload string
	offset  int64
	seq     uint64
}

func (w *world) keep(payloads []alog.MultipartPayload, withOffsets bool) error {
	offsets := make([]int64, len(payloads))
	if !withOffsets {
		offsets = nil
	}

	last, err := w.log.AppendMultipartBatchOffsets(payloads, offsets)
	if err != nil {
		return w.record(0, err)
	}

	if w.spots == nil {
		w.spots = map[string]spot{}
	}

	first := last - uint64(len(payloads)) + 1
	for i, p := range payloads {
		if offsets != nil {
			key := string(p[0])
			w.spots[key] = spot{payload: key, seq: first + uint64(i), offset: offsets[i]}
		}
	}

	return w.record(last, nil)
}

func batchOf(payloads []string) []alog.MultipartPayload {
	batch := make([]alog.MultipartPayload, len(payloads))
	for i, p := range payloads {
		batch[i] = alog.MultipartPayload{[]byte(p)}
	}

	return batch
}

// sortedSpots returns the kept records in the order they were written.
func (w *world) sortedSpots() []spot {
	spots := make([]spot, 0, len(w.spots))
	for _, s := range w.spots {
		spots = append(spots, s)
	}

	sort.Slice(spots, func(i, j int) bool { return spots[i].seq < spots[j].seq })

	return spots
}

func (w *world) spotOf(payload string) (spot, error) {
	s, ok := w.spots[payload]
	if !ok {
		return spot{}, fmt.Errorf("no offset was kept for %q", payload)
	}

	return s, nil
}

func (w *world) seekSpot(s spot, shift int64) error {
	sc, err := w.currentScanner()
	if err != nil {
		return err
	}

	w.err = sc.SeekOffset(s.seq, s.offset+shift)

	return nil
}

func (w *world) registerOffsets(sc *godog.ScenarioContext) {
	list := `((?:"[^"]*"(?:, )?)+)`

	sc.Step(`^I append the batch `+list+` and keep the offsets$`, func(l string) error {
		if err := w.keep(batchOf(quoted(l)), true); err != nil {
			return err
		}

		return w.err
	})
	sc.Step(`^I append "([^"]*)" and keep the offset$`, func(p string) error {
		if err := w.keep(batchOf([]string{p}), true); err != nil {
			return err
		}

		return w.err
	})
	sc.Step(`^I append (\d+) records of (\d+) bytes and keep the offsets$`, func(count, size int) error {
		for i := range count {
			if err := w.keep(batchOf([]string{string(payloadOf(size, i))}), true); err != nil {
				return err
			}
			if w.err != nil {
				return w.err
			}
		}

		return nil
	})
	sc.Step(`^I try to append the batch `+list+` with room for (\d+) offsets?$`, func(l string, room int) error {
		batch := batchOf(quoted(l))
		w.lastSeq, w.err = w.log.AppendMultipartBatchOffsets(batch, make([]int64, room))

		return nil
	})
	sc.Step(`^I try to append the batch `+list+` without room for offsets$`, func(l string) error {
		return w.keep(batchOf(quoted(l)), false)
	})
	sc.Step(`^the append fails$`, func() error {
		if w.err == nil {
			return errors.New("the append succeeded")
		}

		return nil
	})
	sc.Step(`^the append succeeds$`, func() error { return w.err })

	sc.Step(`^I seek to the offset of "([^"]*)"$`, func(p string) error {
		s, err := w.spotOf(p)
		if err != nil {
			return err
		}

		return w.seekSpot(s, 0)
	})
	sc.Step(`^I seek to the offset of "([^"]*)" plus (\d+) bytes?$`, func(p string, shift int64) error {
		s, err := w.spotOf(p)
		if err != nil {
			return err
		}

		return w.seekSpot(s, shift)
	})
	sc.Step(`^I seek to sequence (\d+) at offset (\d+)$`, func(seq uint64, offset int64) error {
		return w.seekSpot(spot{seq: seq, offset: offset}, 0)
	})

	sc.Step(`^scanning fails$`, func() error {
		if w.err != nil {
			return w.err
		}

		sc, err := w.currentScanner()
		if err != nil {
			return err
		}

		if sc.Scan() {
			return fmt.Errorf("scan found %q at sequence %d", sc.Data(), sc.LastSequence())
		}
		if sc.Err() == nil {
			return errors.New("scan found nothing, without an error")
		}

		return nil
	})

	sc.Step(`^the offsets are increasing$`, func() error {
		spots := w.sortedSpots()
		for i := 1; i < len(spots); i++ {
			if spots[i].offset <= spots[i-1].offset {
				return fmt.Errorf("offset of %q (%d) is not after the offset of %q (%d)",
					spots[i].payload, spots[i].offset, spots[i-1].payload, spots[i-1].offset)
			}
		}

		return nil
	})
	sc.Step(`^the offset of "([^"]*)" is after the offset of "([^"]*)"$`, func(later, earlier string) error {
		l, err := w.spotOf(later)
		if err != nil {
			return err
		}

		e, err := w.spotOf(earlier)
		if err != nil {
			return err
		}

		if l.offset <= e.offset {
			return fmt.Errorf("%q is at %d, %q is at %d", later, l.offset, earlier, e.offset)
		}

		return nil
	})

	sc.Step(`^scanning finds every kept record at its offset$`, func() error {
		s, err := alog.NewScanner(w.log)
		if err != nil {
			return err
		}
		defer s.Close() //nolint:errcheck

		found := 0
		for s.Scan() {
			kept, ok := w.spots[string(s.Data())]
			if !ok {
				continue
			}

			if s.LastOffset() != kept.offset || s.LastSequence() != kept.seq {
				return fmt.Errorf("%q is at sequence %d, offset %d, it was kept as %d, %d",
					kept.payload, s.LastSequence(), s.LastOffset(), kept.seq, kept.offset)
			}

			found++
		}

		if err := s.Err(); err != nil {
			return err
		}
		if found != len(w.spots) {
			return fmt.Errorf("found %d of the %d kept records", found, len(w.spots))
		}

		return nil
	})
	sc.Step(`^every kept record is found by its offset in a scattered order$`, func() error {
		spots := w.sortedSpots()

		s, err := alog.NewScanner(w.log)
		if err != nil {
			return err
		}
		defer s.Close() //nolint:errcheck

		n := len(spots)
		for i := range n {
			want := spots[(i*7+3)%n]

			if err := s.SeekOffset(want.seq, want.offset); err != nil {
				return fmt.Errorf("seek to %q: %w", want.payload, err)
			}
			if !s.Scan() {
				return fmt.Errorf("scan at %q: %v", want.payload, s.Err())
			}
			if s.LastSequence() != want.seq || string(s.Data()) != want.payload {
				return fmt.Errorf("at the offset of %q found %q at sequence %d", want.payload, s.Data(), s.LastSequence())
			}
		}

		return nil
	})
}
