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

package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTempFile(t *testing.T) *os.File {
	t.Helper()

	f, err := os.OpenFile(filepath.Join(t.TempDir(), "file.bin"), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	return f
}

func TestPreallocate(t *testing.T) {
	t.Parallel()

	const size = 64 << 10

	t.Run("sets the length and reads back zeros", func(t *testing.T) {
		t.Parallel()

		f := newTempFile(t)
		require.NoError(t, preallocate(f, size))

		info, err := f.Stat()
		require.NoError(t, err)
		require.Equal(t, int64(size), info.Size())

		got := make([]byte, size)
		_, err = f.ReadAt(got, 0)
		require.NoError(t, err)
		require.Equal(t, make([]byte, size), got)
	})

	t.Run("leaves room to write within the size", func(t *testing.T) {
		t.Parallel()

		f := newTempFile(t)
		require.NoError(t, preallocate(f, size))

		_, err := f.WriteAt([]byte("hello"), 1000)
		require.NoError(t, err)

		got := make([]byte, 5)
		_, err = f.ReadAt(got, 1000)
		require.NoError(t, err)
		require.Equal(t, []byte("hello"), got)

		info, err := f.Stat()
		require.NoError(t, err)
		require.Equal(t, int64(size), info.Size(), "writing within the size must not grow the file")
	})
}

func TestSyncFile(t *testing.T) {
	t.Parallel()

	f := newTempFile(t)

	_, err := f.WriteAt([]byte("durable"), 0)
	require.NoError(t, err)
	require.NoError(t, syncFile(f))
}
