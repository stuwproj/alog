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
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzMetaSlot_Basics(f *testing.F) {
	f.Add(make([]byte, 32))

	f.Fuzz(func(t *testing.T, raw []byte) {
		// normalize to one full metadata slot buffer
		if len(raw) < 32 {
			raw = append(raw, make([]byte, 32-len(raw))...)
		}
		raw = raw[:32]

		var m MetaSlot
		copy(m[:], raw)

		// probe accessors and validation on arbitrary slot bytes
		_ = m.Valid()
		_ = m.ReadOffset()
		_ = m.Generation()
		_ = m.Checksum()

		// update and validate checksum
		m.updateCheckSum()
		require.True(t, m.Valid())

		// generation increments should produce a valid checksummed slot
		bumped := m.With(0, 0, 0, 0, 0, 0)
		require.Equal(t, m.Generation()+1, bumped.Generation())
		require.True(t, bumped.Valid())

		m.Update(100, 200, 1, FlagCheckpointStateInitialized, 7, 42)

		require.Equal(t, uint64(100), m.StartSequence())
		require.Equal(t, int64(200), m.ReadOffset())
		require.Equal(t, uint16(1), m.Generation())
		require.Equal(t, FlagCheckpointStateInitialized, m.Flags())
		require.True(t, m.Valid())
	})
}

func FuzzHeader_Basics(f *testing.F) {
	f.Add(make([]byte, HeaderSize))
	seed := NewHeader(7, 0)
	f.Add(seed[:])

	f.Fuzz(func(t *testing.T, raw []byte) {
		// normalize to one full header-size buffer
		if len(raw) < HeaderSize {
			raw = append(raw, make([]byte, HeaderSize-len(raw))...)
		}

		raw = raw[:HeaderSize]

		var h Header
		copy(h[:], raw)

		// probe generic validation and metadata decoding on arbitrary bytes
		_ = h.Valid()

		if m, err := h.Meta(); err == nil {
			// if Meta() succeeds, the returned metadata must be valid
			require.True(t, m.Valid())
		}

		// generated headers must be self-consistent
		generated := NewHeader(11, 0)
		require.True(t, generated.Valid())

		m, err := generated.Meta()
		require.NoError(t, err)
		require.True(t, m.Valid())

		// mutating header bytes should invalidate checksum/integrity
		mutated := generated
		mutated[0] ^= 0xFF

		require.False(t, mutated.Valid())
	})
}

// func FuzzHeader_MetadataSlotSelection(f *testing.F) {
// 	f.Add(uint64(12), uint16(1), uint16(2), byte(0))
//
// 	f.Fuzz(func(t *testing.T, startSeq uint64, genA uint16, genB uint16, slotSelect byte) {
// 		// start from a known-good header with two valid metadata slots
// 		h := NewHeader(startSeq)
//
// 		a := MetaSlot(h[8:32]).With(0, 0, 0, 0)
// 		b := MetaSlot(h[32:56]).With(0, 0, 0, 0)
//
// 		// force explicit generation values and recompute checksums
// 		// offset  size  field
// 		// 0       8     start sequence
// 		// 8       8     read byte offset
// 		// 16      2     slot generation
// 		// 18      2     compat
// 		// 20      4     crc
// 		binary.BigEndian.PutUint16(a[16:18], genA)
// 		binary.BigEndian.PutUint32(a[20:24], a.Checksum())
// 		binary.BigEndian.PutUint16(b[16:18], genB)
// 		binary.BigEndian.PutUint32(b[20:24], b.Checksum())
//
// 		if slotSelect&1 != 0 {
// 			// corrupt slot A
// 			a[0] ^= 0x01
// 		}
// 		if slotSelect&2 != 0 {
// 			// corrupt slot B
// 			b[0] ^= 0x01
// 		}
//
// 		// write mutated slots back into the header image
// 		copy(h[8:32], a[:])
// 		copy(h[32:56], b[:])
//
// 		// have the header select the correct slot based on validity
// 		meta, err := h.Meta()
//
// 		// assert at least one slot is valid
// 		if !a.Valid() && !b.Valid() {
// 			require.Error(t, err)
// 			return
// 		}
//
// 		require.NoError(t, err)
// 		require.True(t, meta.Valid())
// 	})
// }
