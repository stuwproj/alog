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

func FuzzOSFile_WritePartsAt(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			raw = raw[:1<<20]
		}

		fs := NewOSFileSystem(false)
		path := filepath.Join(t.TempDir(), "fuzz-writev.bin")
		file, err := fs.Create(path, 0)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, file.Close()) })

		parts := make([][]byte, 0, len(raw)+1)
		want := make([]byte, 0, len(raw))
		for i, b := range raw {
			if i%11 == 0 {
				parts = append(parts, nil)
			}

			if i%3 == 0 {
				parts = append(parts, []byte{})
			}

			p := []byte{b}
			parts = append(parts, p)
			want = append(want, p...)
		}

		n, err := file.WritePartsAt(parts, 0)
		require.NoError(t, err)
		require.Equal(t, len(want), n)
		require.NoError(t, file.Sync())

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
}
