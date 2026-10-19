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
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/storage"
	modframe "stuw.net/frame"
	"github.com/stretchr/testify/require"
)

const eofTestStride = 4

// segmentLayout lists the physical offsets of frames in a segment.
type segmentLayout struct {
	records     []int64
	checkpoints []int64
	eof         int64
}

// newEOFTestSegment creates a preallocated segment with the given number of
// single-record appends and a checkpoint every eofTestStride records:
//
//	r0 r1 r2 r3 cp r4 r5 r6 r7 cp r8 r9 eof
func newEOFTestSegment(t *testing.T, records int) (*Segment, *[]string) {
	t.Helper()

	loader := NewLoader(NewFactory(4096, false, eofTestStride), t.TempDir(), 0, storage.NewOSFileSystem(false))

	s, err := loader.Create(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	for i := range records {
		_, err = s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
		require.NoError(t, err)
	}

	logs := &[]string{}
	s.logger = func(msg string) { *logs = append(*logs, msg) }

	return s, logs
}

// newBatchTestSegment creates a preallocated segment from batches of
// perBatch records each. Records are named r0, r1, ...
func newBatchTestSegment(t *testing.T, stride uint16, batches, perBatch int) (*Segment, *[]string) {
	t.Helper()

	loader := NewLoader(NewFactory(4096, false, stride), t.TempDir(), 0, storage.NewOSFileSystem(false))

	s, err := loader.Create(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	n := 0
	for range batches {
		batch := make([]frame.MultiPartPayload, perBatch)
		for i := range batch {
			batch[i] = frame.NewPayload([]byte(fmt.Sprintf("r%d", n)))
			n++
		}

		_, err = s.AppendBatch(batch)
		require.NoError(t, err)
	}

	logs := &[]string{}
	s.logger = func(msg string) { *logs = append(*logs, msg) }

	return s, logs
}

// headersOf returns the headers of all frames except the EOF frame.
func headersOf(t *testing.T, s *Segment) []frame.Header {
	t.Helper()

	sc, err := s.newScannerAt(int64(header.HeaderSize), 0, nil)
	require.NoError(t, err)

	var hs []frame.Header
	for sc.Scan() {
		hs = append(hs, sc.Header)
	}

	require.ErrorIs(t, sc.Err(), io.EOF)

	return hs
}

// truncateBeforeSeq moves the start of the segment to seq.
func truncateBeforeSeq(t *testing.T, s *Segment, seq uint64) {
	t.Helper()

	if seq >= s.NextSequence() {
		require.NoError(t, s.TruncateBefore(s.writeOffset, s.NextSequence()))
		return
	}

	sc, err := s.NewScanner(0, true)
	require.NoError(t, err)
	require.NoError(t, sc.SeekSequence(seq))
	require.NoError(t, s.TruncateBefore(sc.NextOffset, sc.NextSequence()))
}

func layoutOf(t *testing.T, s *Segment) segmentLayout {
	t.Helper()

	sc, err := s.newScannerAt(int64(header.HeaderSize), 0, nil)
	require.NoError(t, err)

	var l segmentLayout
	for sc.Scan() {
		if sc.Header.IsCheckpoint() {
			l.checkpoints = append(l.checkpoints, sc.LastOffset)
			continue
		}

		l.records = append(l.records, sc.LastOffset)
	}

	require.ErrorIs(t, sc.Err(), io.EOF)
	l.eof = sc.LastOffset

	return l
}

func recovered(logs *[]string) bool {
	for _, msg := range *logs {
		if strings.HasPrefix(msg, "recovering segment tail") {
			return true
		}
	}

	return false
}

// reinit re-runs init and asserts whether tail recovery happened.
func reinit(t *testing.T, s *Segment, logs *[]string, wantRecovery bool) {
	t.Helper()

	*logs = (*logs)[:0]
	require.NoError(t, s.init())
	require.Equal(t, wantRecovery, recovered(logs), "recovery: logs=%v", *logs)
}

// readRecords reads all records with payload validation.
func readRecords(t *testing.T, s *Segment) []string {
	t.Helper()

	sc, err := s.NewScanner(1<<20, false)
	require.NoError(t, err)

	var got []string
	for sc.Scan() {
		if sc.Header.IsCheckpoint() {
			continue
		}

		got = append(got, string(sc.Frame.Payload))
	}

	require.ErrorIs(t, sc.Err(), io.EOF)

	return got
}

func records(n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = fmt.Sprintf("r%d", i)
	}

	return out
}

func TestSegment(t *testing.T) {
	t.Parallel()

	t.Run("init validates eof checkpoint of intact segment", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		reinit(t, s, logs, false)
		require.Equal(t, uint32(10), s.nextWriteIndex)
		require.Equal(t, l.eof, s.writeOffset)
	})

	t.Run("init validates eof checkpoint of empty segment", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 0)

		reinit(t, s, logs, false)
		require.Equal(t, uint32(0), s.nextWriteIndex)
		require.Equal(t, int64(header.HeaderSize), s.writeOffset)
	})

	t.Run("init restores tracker so later checkpoints validate", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		reinit(t, s, logs, false)

		// crosses the next checkpoint using the restored tracker state
		for i := 10; i < 15; i++ {
			_, err := s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
			require.NoError(t, err)
		}

		reinit(t, s, logs, false)
		require.Equal(t, records(15), readRecords(t, s))
	})

	t.Run("init recovers corrupted header in tail run", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		corruptPayloadLengthAtOffset(t, s.Name(), l.records[8])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(8), s.nextWriteIndex)
		require.Equal(t, l.records[8], s.writeOffset)
		require.Equal(t, records(8), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init recovers corrupted index stride in tail run", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		corruptIndexStrideAtOffset(t, s.Name(), l.records[9])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(9), s.nextWriteIndex)
		require.Equal(t, records(9), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init keeps intact records when eof checkpoint is corrupted", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		corruptAtOffset(t, s.Name(), l.eof, []byte{0xFF})

		reinit(t, s, logs, true)
		require.Equal(t, uint32(10), s.nextWriteIndex)
		require.Equal(t, l.eof, s.writeOffset)
		require.Equal(t, records(10), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init does not walk into a zeroed tail", func(t *testing.T) {
		t.Parallel()

		// generation 0 segment: an all-zero header matches the generation
		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		corruptAtOffset(t, s.Name(), l.eof, make([]byte, frame.HeaderSize))

		reinit(t, s, logs, true)
		require.Equal(t, uint32(10), s.nextWriteIndex)
		require.Equal(t, l.eof, s.writeOffset)
		require.Equal(t, records(10), readRecords(t, s))
	})

	t.Run("init rejects eof frame that is not a checkpoint", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		var plain frame.Header
		plain.SetOne(s.meta.FrameGeneration(), 0, nil)
		binary.BigEndian.PutUint16(plain[10:12], frame.FlagEOF)
		plain.SetChecksum(modframe.Checksum(&plain, nil, 0))
		corruptAtOffset(t, s.Name(), l.eof, plain[:])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(10), s.nextWriteIndex)
		require.Equal(t, records(10), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init truncates at first corruption before last checkpoint", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		corruptIndexStrideAtOffset(t, s.Name(), l.records[1])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(1), s.nextWriteIndex)
		require.Equal(t, records(1), readRecords(t, s))
	})

	for _, seq := range []uint64{0, 3, 4, 6, 9} {
		t.Run(fmt.Sprintf("truncate after %d writes valid eof checkpoint", seq), func(t *testing.T) {
			t.Parallel()

			s, logs := newEOFTestSegment(t, 10)

			require.NoError(t, s.TruncateAfter(seq))
			require.Equal(t, seq, s.NextSequence())

			reinit(t, s, logs, false)
			require.Equal(t, seq, s.NextSequence())

			for i := int(seq); i < int(seq)+6; i++ {
				_, err := s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
				require.NoError(t, err)
			}

			reinit(t, s, logs, false)
			require.Equal(t, records(int(seq)+6), readRecords(t, s))
		})
	}

	t.Run("append marks the end of each batch and checkpoints after it", func(t *testing.T) {
		t.Parallel()

		// 7 records is more than the stride, but the checkpoint waits for the end of the batch
		s, _ := newBatchTestSegment(t, 5, 1, 7)
		l := layoutOf(t, s)

		require.Len(t, l.records, 7)
		require.Len(t, l.checkpoints, 1)
		require.Greater(t, l.checkpoints[0], l.records[6])

		hs := headersOf(t, s)
		require.Len(t, hs, 8)

		for i, h := range hs[:7] {
			require.NoError(t, h.CheckFlags())
			require.Equal(t, i == 6, h.IsBatchEnd(), "record %d", i)
		}

		require.True(t, hs[7].IsCheckpoint())
		require.False(t, hs[7].IsBatchEnd())
	})

	t.Run("init drops a torn batch as a whole", func(t *testing.T) {
		t.Parallel()

		// r0 r1 r2 | r3 r4 r5 | r6 r7 r8 | r9 r10 r11, no checkpoints
		s, logs := newBatchTestSegment(t, 100, 4, 3)
		l := layoutOf(t, s)

		// r7 is in the middle of the third batch, r6 before it is intact
		corruptIndexStrideAtOffset(t, s.Name(), l.records[7])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(6), s.nextWriteIndex)
		require.Equal(t, l.records[6], s.writeOffset)
		require.Equal(t, records(6), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init drops a torn batch that spans a checkpoint", func(t *testing.T) {
		t.Parallel()

		// r0 r1 r2 | r3 r4 r5 CP | r6 r7 r8 | r9 r10 r11 CP
		s, logs := newBatchTestSegment(t, 5, 4, 3)
		l := layoutOf(t, s)
		require.Len(t, l.checkpoints, 2)

		corruptIndexStrideAtOffset(t, s.Name(), l.records[10])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(9), s.nextWriteIndex)
		require.Equal(t, records(9), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init drops a batch with missing frames", func(t *testing.T) {
		t.Parallel()

		s, logs := newBatchTestSegment(t, 100, 4, 3)
		l := layoutOf(t, s)

		// the write stopped before the last frame of the last batch: no batch
		// end, no end marker. r9 and r10 are intact, but not confirmed.
		corruptAtOffset(t, s.Name(), l.records[11], make([]byte, l.eof+frame.HeaderSize-l.records[11]))

		reinit(t, s, logs, true)
		require.Equal(t, uint32(9), s.nextWriteIndex)
		require.Equal(t, l.records[9], s.writeOffset)
		require.Equal(t, records(9), readRecords(t, s))

		reinit(t, s, logs, false)
	})

	t.Run("init keeps complete batches when only the end marker is corrupted", func(t *testing.T) {
		t.Parallel()

		s, logs := newBatchTestSegment(t, 100, 4, 3)
		l := layoutOf(t, s)

		corruptAtOffset(t, s.Name(), l.eof, []byte{0xFF})

		reinit(t, s, logs, true)
		require.Equal(t, uint32(12), s.nextWriteIndex)
		require.Equal(t, records(12), readRecords(t, s))
	})

	t.Run("truncate after at a batch boundary writes no checkpoint", func(t *testing.T) {
		t.Parallel()

		s, logs := newBatchTestSegment(t, 100, 2, 3)
		l := layoutOf(t, s)

		require.NoError(t, s.TruncateAfter(3))
		require.Equal(t, l.records[3], s.writeOffset)
		require.Empty(t, layoutOf(t, s).checkpoints)

		reinit(t, s, logs, false)
		require.Equal(t, records(3), readRecords(t, s))
	})

	t.Run("truncate after inside a batch writes a checkpoint", func(t *testing.T) {
		t.Parallel()

		s, logs := newBatchTestSegment(t, 100, 2, 3)
		l := layoutOf(t, s)

		require.NoError(t, s.TruncateAfter(4))
		require.Equal(t, uint64(4), s.NextSequence())
		require.Equal(t, l.records[4]+frame.HeaderSize, s.writeOffset)

		after := layoutOf(t, s)
		require.Equal(t, []int64{l.records[4]}, after.checkpoints)
		require.Equal(t, s.writeOffset, after.eof)

		reinit(t, s, logs, false)
		require.Equal(t, records(4), readRecords(t, s))

		// appending continues after the checkpoint
		_, err := s.AppendOne([]byte("r4"))
		require.NoError(t, err)

		reinit(t, s, logs, false)
		require.Equal(t, records(5), readRecords(t, s))
	})

	t.Run("a torn append after a mid batch truncation keeps the truncated records", func(t *testing.T) {
		t.Parallel()

		s, logs := newBatchTestSegment(t, 100, 2, 3)

		// r3 is the first record of the second batch. Its batch end is cut away,
		// but the record itself is confirmed to the caller and must survive.
		require.NoError(t, s.TruncateAfter(4))

		_, err := s.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte("x0")),
			frame.NewPayload([]byte("x1")),
		})
		require.NoError(t, err)

		l := layoutOf(t, s)
		corruptIndexStrideAtOffset(t, s.Name(), l.records[len(l.records)-1])

		reinit(t, s, logs, true)
		require.Equal(t, uint32(4), s.nextWriteIndex)
		require.Equal(t, records(4), readRecords(t, s))
	})

	t.Run("truncate after next sequence is a no-op", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		offset := s.writeOffset

		require.NoError(t, s.TruncateAfter(10))
		require.Equal(t, offset, s.writeOffset)

		reinit(t, s, logs, false)
	})

	for _, seq := range []uint64{2, 4, 6, 10} {
		t.Run(fmt.Sprintf("truncate before %d persists checkpoint state", seq), func(t *testing.T) {
			t.Parallel()

			s, logs := newEOFTestSegment(t, 10)

			sc, err := s.NewScanner(0, true)
			require.NoError(t, err)

			if seq < s.NextSequence() {
				require.NoError(t, sc.SeekSequence(seq))
				require.NoError(t, s.TruncateBefore(sc.NextOffset, sc.NextSequence()))
			} else {
				require.NoError(t, s.TruncateBefore(s.writeOffset, s.NextSequence()))
			}

			reinit(t, s, logs, false)

			// cross the next checkpoint, then validate the chain from the new start
			for i := 10; i < 16; i++ {
				_, err = s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
				require.NoError(t, err)
			}

			reinit(t, s, logs, false)
			require.Equal(t, records(16)[seq:], readRecords(t, s))
		})
	}

	for _, seq := range []uint64{2, 4, 6, 10} {
		t.Run(fmt.Sprintf("append directly after truncate before %d keeps the checkpoint chain valid", seq), func(t *testing.T) {
			t.Parallel()

			s, logs := newEOFTestSegment(t, 10)
			tail := s.tracker.State()

			truncateBeforeSeq(t, s, seq)
			require.Equal(t, tail, s.tracker.State(), "moving the head must not change the tail state")

			// no reload in between: the checkpoint that this writes uses the tracker as it is
			appendRecords(t, s, 10, 16)

			reinit(t, s, logs, false)
			require.Equal(t, records(16)[seq:], readRecords(t, s))
		})
	}

	t.Run("append batches after truncate before keeps the checkpoint chain valid", func(t *testing.T) {
		t.Parallel()

		// r0 r1 r2 | r3 r4 r5 CP | r6 r7 r8 | r9 r10 r11 CP
		s, logs := newBatchTestSegment(t, 5, 4, 3)
		tail := s.tracker.State()

		truncateBeforeSeq(t, s, 4)
		require.Equal(t, tail, s.tracker.State())

		for i := 12; i < 24; i += 3 {
			_, err := s.AppendBatch([]frame.MultiPartPayload{
				frame.NewPayload([]byte(fmt.Sprintf("r%d", i))),
				frame.NewPayload([]byte(fmt.Sprintf("r%d", i+1))),
				frame.NewPayload([]byte(fmt.Sprintf("r%d", i+2))),
			})
			require.NoError(t, err)
		}

		reinit(t, s, logs, false)
		require.Equal(t, records(24)[4:], readRecords(t, s))
	})

	t.Run("reset persists checkpoint state", func(t *testing.T) {
		t.Parallel()

		s, logs := newEOFTestSegment(t, 10)
		l := layoutOf(t, s)

		require.NoError(t, s.Reset(0, l.records[6]))
		require.False(t, recovered(logs))

		reinit(t, s, logs, false)
	})
}

func TestLoader_Recycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	loader := NewLoader(NewFactory(4096, false, eofTestStride), dir, 0, storage.NewOSFileSystem(false))

	s, err := loader.Create(0)
	require.NoError(t, err)

	for i := range 10 {
		_, err = s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
		require.NoError(t, err)
	}

	path := s.Name()
	require.NoError(t, s.Close())

	recycled, err := loader.Recycle(path, 100)
	require.NoError(t, err)
	require.NoError(t, recycled.Close())

	segments, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, segments, 1)
	t.Cleanup(func() { _ = segments[0].Close() })

	reloaded := segments[0]
	require.Equal(t, filepath.Base(path), filepath.Base(reloaded.Name()))
	require.Equal(t, uint64(100), reloaded.NextSequence())
	require.Equal(t, int64(header.HeaderSize), reloaded.writeOffset)

	_, err = reloaded.AppendOne([]byte("x"))
	require.NoError(t, err)
	require.Equal(t, uint64(101), reloaded.NextSequence())

	// a loader without a factory cannot create the segment, and says so
	_, err = NewLoader(nil, dir, 0, storage.NewOSFileSystem(false)).Recycle(path, 0)
	require.ErrorContains(t, err, "factory")
}

func TestLoader_RecycleAfterReadPositionMoves(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	loader := NewLoader(NewFactory(4096, false, eofTestStride), dir, 0, storage.NewOSFileSystem(false))

	s, err := loader.Create(0)
	require.NoError(t, err)

	for i := range 10 {
		_, err = s.AppendOne([]byte(fmt.Sprintf("r%d", i)))
		require.NoError(t, err)
	}

	l := layoutOf(t, s)
	for i := 1; i <= 6; i++ {
		require.NoError(t, s.Reset(uint64(i), l.records[i]))
	}

	path := s.Name()
	gen := s.meta.FrameGeneration()
	require.NoError(t, s.Close())

	recycled, err := loader.Recycle(path, 100)
	require.NoError(t, err)
	require.NoError(t, recycled.Close())

	segments, err := loader.Load()
	require.NoError(t, err)
	require.Len(t, segments, 1)
	t.Cleanup(func() { _ = segments[0].Close() })

	reloaded := segments[0]
	require.Equal(t, uint64(100), reloaded.StartSequence())
	require.Equal(t, gen+1, reloaded.meta.FrameGeneration())
	require.Equal(t, uint64(100), reloaded.NextSequence())
}
