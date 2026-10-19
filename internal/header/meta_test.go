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

func TestMetaSlot_Update(t *testing.T) {
	t.Parallel()
	var m MetaSlot

	m.Update(100, 200, 1, FlagCheckpointStateInitialized, 7, 42)

	require.Equal(t, uint64(100), m.StartSequence())
	require.Equal(t, int64(200), m.ReadOffset())
	require.Equal(t, uint16(1), m.Generation())
	require.Equal(t, FlagCheckpointStateInitialized, m.Flags())
	require.Equal(t, uint16(7), m.CheckpointRemaining())
	require.Equal(t, uint32(42), m.CheckpointCRC())
}
