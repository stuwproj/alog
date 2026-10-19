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

package header

import (
	"encoding/binary"
	"testing"

	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestNewHeader(t *testing.T) {
	t.Parallel()

	t.Run("encodes magic version and start sequence", func(t *testing.T) {
		t.Parallel()

		const start = uint64(987654321)

		h := NewHeader(start, 0)

		require.Equal(t, SegmentMagic, h.Magic())
		require.Equal(t, SegmentVersion, h.Version())

		meta, err := h.Meta()
		require.NoError(t, err)
		require.Equal(t, start, meta.StartSequence())
	})
}

func TestHeader_Valid(t *testing.T) {
	t.Parallel()

	t.Run("returns true for generated header", func(t *testing.T) {
		t.Parallel()

		h := NewHeader(1, 0)
		require.True(t, h.Valid())
	})

	t.Run("returns false for invalid magic", func(t *testing.T) {
		t.Parallel()

		h := NewHeader(1, 0)
		h[0] ^= 0xFF
		require.False(t, h.Valid())
	})

	t.Run("returns false for invalid version", func(t *testing.T) {
		t.Parallel()

		h := NewHeader(1, 0)
		h[4] ^= 0xFF
		require.False(t, h.Valid())
	})
}

func newHeaderFile(t *testing.T, h Header) (storage.File, MetaSlot) {
	t.Helper()

	f, err := storage.NewOSFileSystem(false).Create(t.TempDir()+"/segment.bin", -1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	_, err = f.Write(h[:])
	require.NoError(t, err)
	require.NoError(t, f.Sync())

	m, err := h.Meta()
	require.NoError(t, err)

	return f, m
}

func loadMeta(t *testing.T, f storage.File) MetaSlot {
	t.Helper()

	var loaded Header
	_, err := f.ReadAt(loaded[:], 0)
	require.NoError(t, err)
	require.True(t, loaded.Valid())

	meta, err := loaded.Meta()
	require.NoError(t, err)

	return meta
}

func TestMoveReadPosition(t *testing.T) {
	t.Parallel()

	t.Run("persists the position and keeps everything else", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(10, 2))

		require.NoError(t, MoveReadPosition(f, ReadPosition{
			Seq:         42,
			ReadOffset:  128,
			CPRemaining: 9,
			CPCRC:       123,
		}, &m))

		meta := loadMeta(t, f)
		require.Equal(t, uint64(42), meta.StartSequence())
		require.Equal(t, int64(128), meta.ReadOffset())
		require.Equal(t, uint16(9), meta.CheckpointRemaining())
		require.Equal(t, uint32(123), meta.CheckpointCRC())
		require.Equal(t, uint16(2), meta.FrameGeneration())
		require.NotZero(t, meta.Flags()&FlagCheckpointStateInitialized)
	})

	t.Run("never changes the frame generation", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(0, 5))

		for i := range 6 {
			require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: uint64(i), ReadOffset: int64(100 + i)}, &m))
			require.Equal(t, uint16(5), loadMeta(t, f).FrameGeneration())
		}
	})

	t.Run("rejects a negative offset", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(0, 0))
		require.Error(t, MoveReadPosition(f, ReadPosition{ReadOffset: -1}, &m))
	})

	t.Run("keeps working when the slot generation wraps around", func(t *testing.T) {
		t.Parallel()

		// slot 2 holds generation 65535 (odd), slot 1 holds the generation that follows it, 0
		h := NewHeader(10, 0)
		older, newer := MetaSlot(h[40:72]), MetaSlot(h[8:40])

		binary.BigEndian.PutUint16(older[16:18], 65535)
		binary.BigEndian.PutUint64(older[0:8], 20)
		older.updateCheckSum()

		binary.BigEndian.PutUint16(newer[16:18], 0)
		binary.BigEndian.PutUint64(newer[0:8], 21)
		newer.updateCheckSum()

		copy(h[8:40], newer[:])
		copy(h[40:72], older[:])

		f, m := newHeaderFile(t, h)
		require.Equal(t, uint64(21), m.StartSequence(), "generation 0 follows 65535")
		require.True(t, m.NewerThan(older))

		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 22, ReadOffset: 100}, &m))
		require.Equal(t, uint64(22), loadMeta(t, f).StartSequence())
		require.Equal(t, uint16(1), loadMeta(t, f).Generation())

		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 23, ReadOffset: 100}, &m))
		require.Equal(t, uint64(23), loadMeta(t, f).StartSequence())
	})

	t.Run("a torn slot falls back to the previous one, and the next update repairs it", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(10, 0))
		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 11, ReadOffset: 100}, &m))
		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 12, ReadOffset: 200}, &m))

		// the slot of the last update is torn
		var raw Header
		_, err := f.ReadAt(raw[:], 0)
		require.NoError(t, err)

		newest, err := raw.Meta()
		require.NoError(t, err)

		offset := int64(metaSlot1Offset)
		if newest.Generation()%2 == 1 {
			offset = int64(metaSlot2Offset)
		}

		_, err = f.WriteAt([]byte{0xFF, 0xFF, 0xFF}, offset+10)
		require.NoError(t, err)

		previous := loadMeta(t, f)
		require.Equal(t, uint64(11), previous.StartSequence(), "the update before it")

		// loading starts from the older slot, and the next update goes to the torn one
		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 13, ReadOffset: 300}, &previous))
		require.Equal(t, uint64(13), loadMeta(t, f).StartSequence())
	})
}

func TestRecycleFile(t *testing.T) {
	t.Parallel()

	t.Run("replaces every value and bumps the frame generation", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(10, 2))
		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 12, ReadOffset: 500, CPRemaining: 3, CPCRC: 77}, &m))

		require.NoError(t, RecycleFile(f, RecycleUpdate{
			Seq:             100,
			FrameGeneration: 3,
			Flags:           FlagCheckpointStateInitialized,
			CPRemaining:     4096,
		}, &m))

		meta := loadMeta(t, f)
		require.Equal(t, uint64(100), meta.StartSequence())
		require.Equal(t, int64(0), meta.ReadOffset())
		require.Equal(t, uint16(3), meta.FrameGeneration())
		require.Equal(t, uint16(4096), meta.CheckpointRemaining())
		require.Equal(t, uint32(0), meta.CheckpointCRC())
	})

	t.Run("requires the frame generation to be bumped", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(10, 2))

		for _, gen := range []uint16{0, 2, 4} {
			err := RecycleFile(f, RecycleUpdate{Seq: 100, FrameGeneration: gen}, &m)
			require.ErrorIs(t, err, ErrFrameGenerationNotBumped)
		}

		require.Equal(t, uint64(10), loadMeta(t, f).StartSequence(), "nothing may be written")
	})

	t.Run("the recycled metadata wins on load after many read position moves", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(0, 0))
		for i := range 7 {
			require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: uint64(i), ReadOffset: 100}, &m))
		}

		require.NoError(t, RecycleFile(f, RecycleUpdate{Seq: 500, FrameGeneration: 1}, &m))
		require.Equal(t, uint64(500), loadMeta(t, f).StartSequence())

		require.NoError(t, MoveReadPosition(f, ReadPosition{Seq: 501, ReadOffset: 200}, &m))
		require.Equal(t, uint64(501), loadMeta(t, f).StartSequence())
	})

	t.Run("the frame generation wraps around", func(t *testing.T) {
		t.Parallel()

		f, m := newHeaderFile(t, NewHeader(10, 0xFFFF))

		require.ErrorIs(t, RecycleFile(f, RecycleUpdate{Seq: 100, FrameGeneration: 0xFFFF}, &m), ErrFrameGenerationNotBumped)
		require.NoError(t, RecycleFile(f, RecycleUpdate{Seq: 100, FrameGeneration: 0}, &m))
		require.Equal(t, uint16(0), loadMeta(t, f).FrameGeneration())

		require.NoError(t, RecycleFile(f, RecycleUpdate{Seq: 200, FrameGeneration: 1}, &m))
		require.Equal(t, uint64(200), loadMeta(t, f).StartSequence())
	})
}
