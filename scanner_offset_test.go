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

package alog_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"stuw.net/alog"
)

func TestScannerSeekOffset(t *testing.T) {
	opts := alog.DefaultOptions()
	opts.Storage.CheckpointStride = 4
	opts.Storage.MaxSegmentSize = 4096
	opts.Storage.Preallocate = -1
	opts.Storage.MaxSegments = 0
	opts.Storage.MaxDiskSize = 0

	dir := t.TempDir()
	l, err := alog.New(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	type loc struct {
		seq    uint64
		offset int64
	}

	var (
		locs    []loc
		payload = make(map[uint64]string)
	)

	pad := bytes.Repeat([]byte("x"), 200)

	for b := range 40 {
		n := b%5 + 1
		batch := make([]alog.MultipartPayload, n)
		offsets := make([]int64, n)
		for i := range batch {
			batch[i] = alog.MultipartPayload{[]byte("p"), []byte(fmt.Sprintf("-%d-%d", b, i)), pad}
		}

		last, appendErr := l.AppendMultipartBatchOffsets(batch, offsets)
		if appendErr != nil {
			t.Fatal(appendErr)
		}

		first := last - uint64(n) + 1
		for i := range batch {
			locs = append(locs, loc{seq: first + uint64(i), offset: offsets[i]})
			payload[first+uint64(i)] = fmt.Sprintf("p-%d-%d", b, i) + string(pad)
		}
	}

	sc, err := alog.NewScanner(l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sc.Close() })

	// every offset reported by the append is where the scanner finds the record,
	// also when the order jumps around and crosses segments
	for round := range 3 {
		for i := range locs {
			j := i
			switch round {
			case 1:
				j = len(locs) - 1 - i
			case 2:
				j = (i * 7) % len(locs)
			}

			at := locs[j]
			if err := sc.SeekOffset(at.seq, at.offset); err != nil {
				t.Fatalf("seek %d@%d: %v", at.seq, at.offset, err)
			}
			if !sc.Scan() {
				t.Fatalf("scan %d: %v", at.seq, sc.Err())
			}
			if sc.LastSequence() != at.seq || sc.LastOffset() != at.offset {
				t.Fatalf("at %d@%d got %d@%d", at.seq, at.offset, sc.LastSequence(), sc.LastOffset())
			}
			if got := string(sc.Data()); got != payload[at.seq] {
				t.Fatalf("seq %d = %q, want %q", at.seq, got, payload[at.seq])
			}
		}
	}

	if files, _ := os.ReadDir(dir); len(files) < 3 {
		t.Fatalf("only %d files, the test should cross segments", len(files))
	}

	// a sequence that is not in the log
	if err := sc.SeekOffset(locs[len(locs)-1].seq+10, 100); !errors.Is(err, alog.ErrOutOfBounds) {
		t.Fatalf("seek past the end = %v", err)
	}

	// the scanner is still usable
	at := locs[3]
	if err := sc.SeekOffset(at.seq, at.offset); err != nil || !sc.Scan() || sc.LastSequence() != at.seq {
		t.Fatalf("scanner unusable after out of bounds: %v", sc.Err())
	}
}

func TestAppendOffsetsTooShort(t *testing.T) {
	l, err := alog.New(t.TempDir(), alog.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	_, err = l.AppendMultipartBatchOffsets(
		[]alog.MultipartPayload{{[]byte("a")}, {[]byte("b")}},
		make([]int64, 1),
	)
	if err == nil {
		t.Fatal("expected an error for too few offsets")
	}
}
