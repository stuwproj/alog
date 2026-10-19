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

package frame

import (
	"encoding/binary"
	"io"
	"testing"

	modframe "stuw.net/frame"
	"github.com/stretchr/testify/require"
)

func headerWithFlags(flags uint16) (h Header) {
	binary.BigEndian.PutUint16(h[10:12], flags)

	return h
}

func TestHeader_CheckFlags(t *testing.T) {
	t.Parallel()

	t.Run("accepts every kind of frame", func(t *testing.T) {
		t.Parallel()

		for name, flags := range map[string]uint16{
			"data":           FlagData,
			"data batch end": FlagData | FlagBatchEnd,
			"checkpoint":     FlagCheckpoint,
			"eof checkpoint": FlagCheckpoint | FlagEOF,
		} {
			require.NoError(t, headerWithFlags(flags).CheckFlags(), name)
		}
	})

	t.Run("reports a header without flags as unexpected eof", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, Header{}.CheckFlags(), io.ErrUnexpectedEOF)
	})

	t.Run("rejects other combinations", func(t *testing.T) {
		t.Parallel()

		for name, flags := range map[string]uint16{
			"eof without checkpoint":   FlagEOF,
			"batch end without data":   FlagBatchEnd,
			"data and checkpoint":      FlagData | FlagCheckpoint,
			"data and eof":             FlagData | FlagEOF,
			"batch end on checkpoint":  FlagCheckpoint | FlagBatchEnd,
			"unknown bit":              1 << 9,
			"unknown bit with data":    FlagData | 1<<9,
			"eof checkpoint and data":  FlagCheckpoint | FlagEOF | FlagData,
			"eof checkpoint batch end": FlagCheckpoint | FlagEOF | FlagBatchEnd,
		} {
			require.ErrorIs(t, headerWithFlags(flags).CheckFlags(), ErrInvalidFrameHeader, name)
		}
	})
}

func TestHeader_SetBatchEnd(t *testing.T) {
	t.Parallel()

	payload := NewPayload([]byte("ab"), []byte("cde"))

	t.Run("sets data frame fields", func(t *testing.T) {
		t.Parallel()

		var h Header
		h.Set(3, 1, payload)

		require.NoError(t, h.CheckFlags())
		require.False(t, h.IsBatchEnd())
		require.False(t, h.IsCheckpoint())
		require.False(t, h.IsEOF())
		require.Equal(t, uint16(3), h.Generation())
		require.Equal(t, uint16(1), h.IndexStride())
		require.Equal(t, uint32(5), h.PayloadLength())
		require.Equal(t, modframe.Checksum(&h, payload.Bytes(), 0), h.Checksum())
	})

	t.Run("covers the batch end flag by the checksum", func(t *testing.T) {
		t.Parallel()

		var last, plain Header
		last.SetBatchEnd(3, 1, payload)
		plain.Set(3, 1, payload)

		require.NoError(t, last.CheckFlags())
		require.True(t, last.IsBatchEnd())
		require.NotEqual(t, plain.Checksum(), last.Checksum())

		// clearing the flag in a stored header is detected
		tampered := last
		binary.BigEndian.PutUint16(tampered[10:12], FlagData)
		require.NotEqual(t, tampered.Checksum(), modframe.Checksum(&tampered, payload.Bytes(), 0))
	})

	t.Run("single part payloads are data frames", func(t *testing.T) {
		t.Parallel()

		var h Header
		h.SetOne(1, 1, []byte("x"))

		require.NoError(t, h.CheckFlags())
		require.False(t, h.IsBatchEnd())
	})
}
