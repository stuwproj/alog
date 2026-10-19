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
	"fmt"
	"io"
	"os"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/scanner"
	"stuw.net/alog/internal/storage"
	"stuw.net/alog/internal/testutil"
	"github.com/stretchr/testify/require"
)

const (
	corruptPayload = "payload"
	corruptLength  = "length"
	corruptIndex   = "index"
)

type corrupt struct {
	typ   string
	index int
}

type args struct {
	corrupts         []corrupt
	prealloc         int64
	checkpointStride uint16
	seed             int
}

type expected struct {
	err         error
	checkpoints map[uint32]int64
	assertState bool
}

type testCase struct {
	name     string
	expected expected
	args     args
}

var scenarios = []testCase{
	{
		name: "seeded with prealloc",
		args: args{
			prealloc:         1024,
			seed:             18, // append 18 batches of 3 frames each
			checkpointStride: 9,  // every 9 batches, we expect a checkpoint, so 2
		},
		expected: expected{
			assertState: true,
			checkpoints: map[uint32]int64{
				9:  234,
				18: 412,
				27: 590,
				36: 768,
				45: 946,
				54: 1124,
			},
		},
	},
	{
		name: "seeded without prealloc",
		args: args{
			prealloc:         -1,
			seed:             8, // append 8 batches of 3 frames each
			checkpointStride: 2,
		},
		expected: expected{
			assertState: true,
			// batches of 3 frames: checkpoints are only written at batch boundaries
			checkpoints: map[uint32]int64{
				3:  126,
				6:  196,
				9:  266,
				12: 336,
				15: 406,
				18: 476,
				21: 546,
				24: 616,
			},
		},
	},
	{
		name: "empty without prealloc",
		args: args{
			prealloc: -1,
			seed:     0,
		},
		expected: expected{
			assertState: true,
			checkpoints: map[uint32]int64{},
		},
	},
	{
		name: "empty with prealloc",
		args: args{
			prealloc:         1024,
			checkpointStride: 7,
			seed:             0,
		},
		expected: expected{
			assertState: true,
			checkpoints: map[uint32]int64{},
		},
	},
	{
		name: "no checkpoints, corrupt payload length of first frame of second batch",
		args: args{
			prealloc: -1,
			seed:     2,
			corrupts: []corrupt{
				{index: 3, typ: corruptPayload},
			},
		},
		expected: expected{
			assertState: false,
			checkpoints: map[uint32]int64{},
			err:         nil,
		},
	},
	{
		name: "corrupt payload length of first frame of batch",
		args: args{
			prealloc: -1,
			seed:     2,
			corrupts: []corrupt{
				{index: 0, typ: corruptLength},
			},
		},
		expected: expected{
			assertState: false,
			checkpoints: map[uint32]int64{},
			err:         nil,
		},
	},
	{
		name: "corrupt index of first frame of batch",
		args: args{
			prealloc: -1,
			seed:     2,
			corrupts: []corrupt{
				{index: 0, typ: corruptIndex},
			},
		},
		expected: expected{
			assertState: false,
			checkpoints: map[uint32]int64{},
			err:         nil,
		},
	},
}

func TestSegment_Init(t *testing.T) {
	for _, tt := range scenarios {
		t.Run(tt.name, func(t *testing.T) {
			s := createInitTestSegment(t, tt)

			err := s.init()

			runInitAssertions(t, s, err, tt)
		})
	}
}

func BenchmarkSegment_Init(b *testing.B) {
	b.Run("roundTrip", func(b *testing.B) {
		for _, tt := range scenarios {
			b.Run(tt.name, func(b *testing.B) {
				b.ReportAllocs()

				// creating the segment dwarfs init, which would make
				// b.Loop run far too many iterations
				p := testutil.NewPadder(b)

				for b.Loop() {
					p.Pause()

					s := createInitTestSegment(b, tt)

					p.Resume()
					err := s.init()
					p.Pause()

					runInitAssertions(b, s, err, tt)
					require.NoError(b, s.Close())

					p.Resume()
				}

				p.Report()
			})
		}
	})

	b.Run("repeated", func(b *testing.B) {
		for _, tt := range scenarios {
			if tt.expected.err != nil {
				// can't benchmark these because they corrupt the segment
				// and will fail on subsequent runs
				continue
			}

			b.Run(tt.name, func(b *testing.B) {
				b.ReportAllocs()
				s := createInitTestSegment(b, tt)

				// creating the segment dwarfs init, which would make
				// b.Loop run far too many iterations
				p := testutil.NewPadder(b)

				for b.Loop() {
					err := s.init()
					p.Pause()

					p.Pause()
					runInitAssertions(b, s, err, tt)
					p.Resume()
				}

				p.Report()
			})
		}
	})
}

func createInitTestSegment(t testing.TB, tt testCase) *Segment {
	args := tt.args
	path := t.TempDir()
	loader := NewLoader(
		NewFactory(args.prealloc, false, args.checkpointStride),
		path,
		0,
		storage.NewOSFileSystem(false),
	)

	s, err := loader.Create(0)
	require.NoError(t, err)

	// close is safe to call twice, so benchmarks can close per iteration
	// instead of holding every segment's fd and mapping until the end
	t.Cleanup(func() {
		_ = s.Close()
		require.NoError(t, os.RemoveAll(path))
	})

	if _, ok := t.(*testing.T); ok {
		s.logger = func(s string) {
			t.Log(s)
		}
	}

	for range args.seed {
		_, err = s.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte("ab")),
			frame.NewPayload([]byte("cd")),
			frame.NewPayload([]byte("ef")),
		})
		require.NoError(t, err)
	}

	for _, c := range args.corrupts {
		hdrOffset, payloadOffset, err := logicalRecordOffsets(s.file.Name(), c.index)
		require.NoError(t, err)

		switch c.typ {
		case corruptLength:
			corruptPayloadLengthAtOffset(t, s.file.Name(), hdrOffset)
		case corruptIndex:
			corruptIndexStrideAtOffset(t, s.file.Name(), hdrOffset)
		case corruptPayload:
			corruptAtOffset(t, s.file.Name(), payloadOffset, []byte{0xFF})
		}
	}

	return s
}

func logicalRecordOffsets(path string, recordIndex int) (logicalHeaderOffset int64, payloadOffset int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	offset := int64(header.HeaderSize)
	logical := 0

	for {
		var ph frame.Header
		_, err = io.ReadFull(io.NewSectionReader(file, offset, int64(frame.HeaderSize)), ph[:])
		if err != nil {
			return 0, 0, err
		}

		if ph.IsEOF() {
			break
		}

		if ph.IsCheckpoint() {
			offset += frame.HeaderSize
			continue
		}

		if logical == recordIndex {
			h := offset
			return h, h + frame.HeaderSize, nil
		}

		logical += int(ph.IndexStride())
		offset += frame.HeaderSize + int64(ph.PayloadLength())
	}

	return 0, 0, fmt.Errorf("record index %d not found", recordIndex)
}

func runInitAssertions(t testing.TB, s *Segment, err error, tt testCase) {
	if tt.expected.err != nil {
		require.ErrorIs(t, err, tt.expected.err, "expected error to match")
	} else {
		require.NoError(t, err, "expected no error")
	}

	if !tt.expected.assertState {
		return
	}

	checkpointMap := s.offsetIndex.Map()
	require.Equal(t, tt.expected.checkpoints, checkpointMap)

	for idx, off := range checkpointMap {
		require.GreaterOrEqualf(
			t,
			off,
			int64(header.HeaderSize),
			"checkpoint offset must be a physical frame offset: idx=%d off=%d", idx, off)

		sc := &scanner.Scanner{
			FrameScanner: &scanner.FrameScanner{
				Limit: frame.HeadersOnly,
				Loader: &scanner.ReaderLoader{
					Reader: s.file,
				},
			},
		}

		require.NoError(t, sc.Seek(idx, off))
		require.Truef(t, sc.Scan(), "checkpoint offset must start at a frame header: idx=%d off=%d err=%v", idx, off, sc.Err())

		h := sc.Frame.Header
		require.Truef(t, h.IsCheckpoint(), "offset index must point to checkpoint header: idx=%d off=%d", idx, off)
		sc.Close()
	}

	frameCount := tt.args.seed * 3
	nextIndex := frameCount
	nextOffset := 0

	// segment header
	nextOffset += header.HeaderSize
	// frame headers + payloads
	nextOffset += frameCount * (frame.HeaderSize + 2)
	// checkpoint headers
	nextOffset += len(tt.expected.checkpoints) * frame.HeaderSize

	require.Equal(t, nextOffset, int(s.writeOffset), "expected next offset to match")
	require.Equal(t, nextIndex, int(s.nextWriteIndex), "expected next index to match")
}
