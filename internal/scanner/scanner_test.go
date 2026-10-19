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

package scanner

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	framecore "stuw.net/frame"
	"github.com/stretchr/testify/require"
)

type stubValidator func(*frame.Header, []byte) error

func (v stubValidator) Validate(h *frame.Header, p []byte) error {
	return v(h, p)
}

func TestPreallocFrameValidator_Validate(t *testing.T) {
	t.Parallel()

	v := NewPreallocFrameValidator(7)

	t.Run("eof marker", func(t *testing.T) {
		t.Parallel()

		h := checkpoint.NewEOF(7, 4, checkpoint.State{})
		err := v.Validate(&h, nil)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("generation mismatch", func(t *testing.T) {
		t.Parallel()

		var h frame.Header
		h.SetOne(8, 1, []byte("x"))
		err := v.Validate(&h, []byte("x"))
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("zeroed header is not a frame", func(t *testing.T) {
		t.Parallel()

		// generation 0 would match, but a header without flags was never written
		require.ErrorIs(t, NewPreallocFrameValidator(0).Validate(&frame.Header{}, nil), io.ErrUnexpectedEOF)
	})

	t.Run("unknown flag combination", func(t *testing.T) {
		t.Parallel()

		var h frame.Header
		h.SetOne(7, 1, []byte("x"))
		binary.BigEndian.PutUint16(h[10:12], frame.FlagData|frame.FlagCheckpoint)

		require.ErrorIs(t, v.Validate(&h, []byte("x")), frame.ErrInvalidFrameHeader)
	})

	t.Run("matching generation", func(t *testing.T) {
		t.Parallel()

		var h frame.Header
		h.SetOne(7, 1, []byte("x"))
		require.NoError(t, v.Validate(&h, []byte("x")))
	})
}

func TestChecksumValidator_Validate(t *testing.T) {
	t.Parallel()

	v := NewChecksumValidator(7)

	t.Run("eof checkpoint", func(t *testing.T) {
		t.Parallel()

		h := checkpoint.NewEOF(7, 4, checkpoint.State{CRC: 42})
		require.ErrorIs(t, v.Validate(&h, nil), io.EOF)
	})

	t.Run("zeroed header is not a frame", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, NewChecksumValidator(0).Validate(&frame.Header{}, []byte{}), io.ErrUnexpectedEOF)
	})

	t.Run("rejects eof frame that is not a checkpoint", func(t *testing.T) {
		t.Parallel()

		var h frame.Header
		h.SetOne(7, 0, nil)
		binary.BigEndian.PutUint16(h[10:12], frame.FlagEOF)
		h.SetChecksum(framecore.Checksum(&h, nil, 0))
		require.ErrorIs(t, v.Validate(&h, nil), frame.ErrInvalidFrameHeader)
	})

	t.Run("rejects corrupted eof checkpoint", func(t *testing.T) {
		t.Parallel()

		h := checkpoint.NewEOF(7, 4, checkpoint.State{CRC: 42})
		h[0] ^= 0xFF
		require.ErrorIs(t, v.Validate(&h, nil), frame.ErrInvalidFrameHeader)
	})
}

func TestCompositeValidator_Validate(t *testing.T) {
	t.Parallel()

	firstErr := errors.New("first")
	secondCalled := false

	v := NewCompositeValidator(
		stubValidator(func(*frame.Header, []byte) error { return firstErr }),
		stubValidator(func(*frame.Header, []byte) error {
			secondCalled = true
			return nil
		}),
	)

	err := v.Validate(&frame.Header{}, nil)
	require.ErrorIs(t, err, firstErr)
	require.False(t, secondCalled)
}

func TestScanner_SeekSequence(t *testing.T) {
	t.Parallel()

	s := &Scanner{BaseSeq: 10}
	err := s.SeekSequence(9)
	require.ErrorIs(t, err, ErrInvalidSequence)
}
