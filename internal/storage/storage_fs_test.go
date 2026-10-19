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
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOSFileSystem_Create(t *testing.T) {
	t.Parallel()

	t.Run("size 1024 allocates 1024B", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "file.bin")

		f, err := fs.Create(path, 1024)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		info, err := fs.Stat(path)
		require.NoError(t, err)
		require.Equal(t, int64(1024), info.Size())
	})

	t.Run("with Open and WritePartsAt roundtrip", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "file.bin")

		f, err := fs.Create(path, -1)
		require.NoError(t, err)

		n, err := f.WritePartsAt([][]byte{[]byte("a"), []byte("bc")}, 0)
		require.NoError(t, err)
		require.Equal(t, 3, n)
		require.NoError(t, f.Sync())
		require.NoError(t, f.Close())

		g, err := fs.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, g.Close())
		})

		buf := make([]byte, 3)
		_, err = g.ReadAt(buf, 0)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), buf)
	})

	t.Run("is exclusive", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "exists.bin")

		f, err := fs.Create(path, -1)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		_, err = fs.Create(path, 0)
		require.ErrorIs(t, err, os.ErrExist)
	})

	t.Run("file name and stat", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "stat.bin")

		f, err := fs.Create(path, -1)
		require.NoError(t, err)
		_, err = f.Write([]byte("abc"))
		require.NoError(t, err)
		require.NoError(t, f.Close())

		g, err := fs.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, g.Close()) })

		require.Equal(t, path, g.Name())
		info, err := fs.Stat(path)
		require.NoError(t, err)
		require.Equal(t, int64(3), info.Size())

		osf, ok := g.(*OSFile)
		require.True(t, ok)
		finfo, err := osf.Stat()
		require.NoError(t, err)
		require.Equal(t, filepath.Base(path), finfo.Name())
	})

	t.Run("WritePartsAt with empty and many parts", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "many.bin")

		f, err := fs.Create(path, -1)
		require.NoError(t, err)

		parts := make([][]byte, 0, 1100)
		var expected []byte
		for i := range 1100 {
			if i%7 == 0 {
				parts = append(parts, nil)
				continue
			}

			p := []byte{byte(i), byte(i >> 2)}
			parts = append(parts, p)
			expected = append(expected, p...)
		}

		n, err := f.WritePartsAt(parts, 0)
		require.NoError(t, err)
		require.Equal(t, len(expected), n)
		require.NoError(t, f.Sync())
		require.NoError(t, f.Close())

		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, expected, raw)
	})

	t.Run("WritePartsAt on closed file returns error", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "closed.bin")

		f, err := fs.Create(path, -1)
		require.NoError(t, err)
		require.NoError(t, f.Close())

		_, err = f.WritePartsAt([][]byte{[]byte("x")}, 0)
		require.Error(t, err)
		require.True(t,
			errors.Is(err, syscall.EBADF) || errors.Is(err, os.ErrClosed),
		)
	})
}

func TestOSFileSystem_SyncDir(t *testing.T) {
	t.Parallel()

	t.Run("missing path", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(false)
		err := fs.SyncDir(filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
	})

	t.Run("read only rejects sync", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(true)
		err := fs.SyncDir(t.TempDir())
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)
	})

	t.Run("read only open rejects writes", func(t *testing.T) {
		t.Parallel()

		wf := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "file.bin")

		f, err := wf.Create(path, -1)
		require.NoError(t, err)
		_, err = f.Write([]byte("abc"))
		require.NoError(t, err)
		require.NoError(t, f.Close())

		rf := NewOSFileSystem(true)
		ro, err := rf.Open(path)
		require.NoError(t, err)

		t.Cleanup(func() {
			require.NoError(t, ro.Close())
		})

		buf := make([]byte, 3)
		_, err = ro.ReadAt(buf, 0)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), buf)

		_, err = ro.Write([]byte("x"))
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)
		err = ro.Sync()
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)
		err = ro.Truncate(0)
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)
	})

	t.Run("read only rejects mutating ops", func(t *testing.T) {
		t.Parallel()

		fs := NewOSFileSystem(true)
		err := fs.MkdirAll(filepath.Join(t.TempDir(), "x"), 0o755)
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)

		path := filepath.Join(t.TempDir(), "file.bin")
		_, err = fs.Create(path, -1)
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)

		err = fs.Remove(filepath.Join(t.TempDir(), "missing"))
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorIs(t, err, ErrReadOnly)
	})
}
