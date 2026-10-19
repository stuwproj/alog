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
	"hash/crc32"
	"testing"

	modframe "stuw.net/frame"
	"github.com/stretchr/testify/require"
)

func TestHeader_Set(t *testing.T) {
	t.Parallel()

	payload := NewPayload([]byte("hello "), []byte("world!"))

	var h Header
	h.Set(2, 7, payload)
	crc := crc32.Update(0, modframe.CRC32cTable, h[:h.ChecksumBytes()])
	crc = crc32.Update(crc, modframe.CRC32cTable, payload.Bytes())
	h.SetChecksum(crc)

	require.Equal(t, int64(len("hello world!")), payload.Size())
	require.Equal(t, uint32(payload.Size()), h.PayloadLength())
	require.Equal(t, uint16(7), h.IndexStride())

	validator := &modframe.CRC32C[Header]{}

	require.NoError(
		t,
		validator.Validate(&h, payload.Bytes()),
	)
}
