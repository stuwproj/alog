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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

var errFS = errors.New("injected filesystem failure")

// failingFS fails operations of an otherwise working file system, and remembers the files it opened.
type failingFS struct {
	storage.FileSystem
	removeErr  func(path string) error
	openErr    func(path string) error
	syncDirErr func() error
	opened     []storage.File
	removed    []string
	mu         sync.Mutex
}

func newFailingFS() *failingFS {
	return &failingFS{FileSystem: storage.NewOSFileSystem(false)}
}

func (f *failingFS) track(file storage.File, err error) (storage.File, error) {
	if err == nil {
		f.mu.Lock()
		f.opened = append(f.opened, file)
		f.mu.Unlock()
	}

	return file, err
}

func (f *failingFS) Open(name string) (storage.File, error) {
	if f.openErr != nil {
		if err := f.openErr(name); err != nil {
			return nil, err
		}
	}

	return f.track(f.FileSystem.Open(name))
}

func (f *failingFS) Create(name string, size int64) (storage.File, error) {
	return f.track(f.FileSystem.Create(name, size))
}

func (f *failingFS) Remove(name string) error {
	if f.removeErr != nil {
		if err := f.removeErr(name); err != nil {
			return err
		}
	}

	f.mu.Lock()
	f.removed = append(f.removed, name)
	f.mu.Unlock()

	return f.FileSystem.Remove(name)
}

func (f *failingFS) SyncDir(path string) error {
	if f.syncDirErr != nil {
		if err := f.syncDirErr(); err != nil {
			return err
		}
	}

	return f.FileSystem.SyncDir(path)
}

// assertAllClosed fails for every file that was opened through the file system and is still open.
func (f *failingFS) assertAllClosed(t *testing.T) {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	for _, file := range f.opened {
		_, err := file.Stat()
		require.Error(t, err, "%s is still open", file.Name())
	}
}

const failureSegmentSize = 200

// newFailingChain creates a chain on fs, and fills it with segments of 3 records each.
func newFailingChain(t *testing.T, fs storage.FileSystem, dir string, maxSegments, segments int) *Chain {
	t.Helper()

	chain, err := NewChain(failureSegmentSize, 0, maxSegments, NewLoader(NewFactory(failureSegmentSize, false, 0), dir, 0, fs))
	require.NoError(t, err)
	t.Cleanup(func() { _ = chain.Close() })

	for i := 0; chain.Snapshot().Len() < segments; i++ {
		_, err = chain.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte(fmt.Sprintf("a%03d", i))),
			frame.NewPayload([]byte(fmt.Sprintf("b%03d", i))),
			frame.NewPayload([]byte(fmt.Sprintf("c%03d", i))),
		})
		require.NoError(t, err)
	}

	return chain
}

// reload loads the directory again, as after a crash, and returns the number of segments.
func reload(t *testing.T, dir string, maxSegments int) int {
	t.Helper()

	chain, err := NewChain(
		failureSegmentSize, 0, maxSegments,
		NewLoader(NewFactory(failureSegmentSize, false, 0), dir, 0, storage.NewOSFileSystem(false)),
	)
	require.NoError(t, err, "the directory must load as a contiguous chain")
	t.Cleanup(func() { _ = chain.Close() })

	return chain.Snapshot().Len()
}

func binFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var names []string

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".bin" {
			names = append(names, entry.Name())
		}
	}

	return names
}

func TestChain_TruncateAfterFailure(t *testing.T) {
	t.Parallel()

	t.Run("segments are removed from the end, so a failure leaves a contiguous chain", func(t *testing.T) {
		t.Parallel()

		dir, fs := t.TempDir(), newFailingFS()
		chain := newFailingChain(t, fs, dir, 0, 5)

		// the first removal works, the second does not
		var removals int

		fs.removeErr = func(string) error {
			removals++
			if removals == 2 {
				return errFS
			}

			return nil
		}

		require.ErrorIs(t, chain.TruncateAfter(0), errFS)

		// the last segment is gone, and the others are still a chain, in memory and on disk
		require.Equal(t, 4, chain.Snapshot().Len())
		require.Len(t, binFiles(t, dir), 4)
		require.Equal(t, 4, reload(t, dir, 0))

		// the truncation can be repeated once the failure is gone
		fs.removeErr = nil
		require.NoError(t, chain.TruncateAfter(0))
		require.Equal(t, 1, chain.Snapshot().Len())
		require.Equal(t, 1, reload(t, dir, 0))
	})

	t.Run("the size accounting follows what was removed", func(t *testing.T) {
		t.Parallel()

		dir, fs := t.TempDir(), newFailingFS()
		chain := newFailingChain(t, fs, dir, 0, 4)

		fs.removeErr = func(string) error {
			if len(fs.removed) == 1 {
				return errFS
			}

			return nil
		}

		require.Error(t, chain.TruncateAfter(0))
		require.Equal(t, int64(3)*failureSegmentSize, chain.DiskSize())

		fs.removeErr = nil
		require.NoError(t, chain.TruncateAfter(0))
		require.Equal(t, int64(failureSegmentSize), chain.DiskSize())
	})
}

func TestChain_TruncateBeforeFailure(t *testing.T) {
	t.Parallel()

	dir, fs := t.TempDir(), newFailingFS()
	chain := newFailingChain(t, fs, dir, 0, 5)

	var removals int

	fs.removeErr = func(string) error {
		removals++
		if removals == 2 {
			return errFS
		}

		return nil
	}

	require.ErrorIs(t, chain.TruncateBefore(12), errFS)

	// the first segment is gone, the rest is a chain
	require.Equal(t, 4, chain.Snapshot().Len())
	require.Equal(t, 4, reload(t, dir, 0))

	fs.removeErr = nil
	require.NoError(t, chain.TruncateBefore(12))
	require.Equal(t, 1, chain.Snapshot().Len(), "sequence 12 starts the fifth segment")
}

func TestChain_AppendBatchRotationFailure(t *testing.T) {
	t.Parallel()

	fill := func(t *testing.T, chain *Chain, from int) error {
		t.Helper()

		for i := from; i < from+3; i++ {
			if _, err := chain.AppendBatch([]frame.MultiPartPayload{
				frame.NewPayload([]byte(fmt.Sprintf("a%03d", i))),
				frame.NewPayload([]byte(fmt.Sprintf("b%03d", i))),
				frame.NewPayload([]byte(fmt.Sprintf("c%03d", i))),
			}); err != nil {
				return err
			}
		}

		return nil
	}

	t.Run("falls back to a new file when recycling fails, and keeps the cause", func(t *testing.T) {
		t.Parallel()

		dir, fs := t.TempDir(), newFailingFS()
		chain := newFailingChain(t, fs, dir, 2, 2)
		before := chain.Snapshot().Tail().NextSequence()

		// recycling opens the file of the oldest segment, which fails once
		head := chain.Snapshot().Head().Name()
		failed := false

		fs.openErr = func(path string) error {
			if path == head && !failed {
				failed = true
				return errFS
			}

			return nil
		}

		require.NoError(t, fill(t, chain, 100))
		require.True(t, failed)
		require.Greater(t, chain.Snapshot().Tail().NextSequence(), before)
		require.Equal(t, 2, chain.Snapshot().Len())
		require.Len(t, binFiles(t, dir), 2)
	})

	t.Run("puts the head back when its file cannot be removed either, and tries again", func(t *testing.T) {
		t.Parallel()

		dir, fs := t.TempDir(), newFailingFS()
		chain := newFailingChain(t, fs, dir, 2, 2)
		disk := chain.DiskSize()

		head := chain.Snapshot().Head().Name()
		fs.openErr = func(path string) error {
			if path == head {
				return errFS
			}

			return nil
		}
		fs.removeErr = func(path string) error {
			if path == head {
				return errFS
			}

			return nil
		}

		err := fill(t, chain, 100)
		require.ErrorIs(t, err, errFS)

		// nothing was lost: the chain still has both segments, and they still count
		require.Equal(t, 2, chain.Snapshot().Len())
		require.Equal(t, disk, chain.DiskSize())
		require.Equal(t, 2, reload(t, dir, 2), "no extra file may be left behind")

		fs.openErr, fs.removeErr = nil, nil
		require.NoError(t, fill(t, chain, 200))
		require.Equal(t, 2, chain.Snapshot().Len())
		require.Len(t, binFiles(t, dir), 2)
	})

	t.Run("a segment that could not be created leaves no file", func(t *testing.T) {
		t.Parallel()

		dir, fs := t.TempDir(), newFailingFS()
		chain := newFailingChain(t, fs, dir, 0, 2)
		files := binFiles(t, dir)

		fs.syncDirErr = func() error { return errFS }

		require.ErrorIs(t, fill(t, chain, 100), errFS)
		require.Equal(t, files, binFiles(t, dir), "the file of the failed segment must be removed")

		fs.syncDirErr = nil
		require.NoError(t, fill(t, chain, 200))
	})
}

func TestChain_NewChainFailure(t *testing.T) {
	t.Parallel()

	t.Run("closes the segments when there are too many", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		newFailingChain(t, storage.NewOSFileSystem(false), dir, 0, 3)

		fs := newFailingFS()
		_, err := NewChain(
			failureSegmentSize, 0, 2,
			NewLoader(NewFactory(failureSegmentSize, false, 0), dir, 0, fs),
		)
		require.ErrorContains(t, err, "too many segments")
		fs.assertAllClosed(t)
	})

	t.Run("closes the segments when the disk budget is exceeded", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		newFailingChain(t, storage.NewOSFileSystem(false), dir, 0, 3)

		fs := newFailingFS()
		_, err := NewChain(
			failureSegmentSize, failureSegmentSize, 0,
			NewLoader(NewFactory(failureSegmentSize, false, 0), dir, 0, fs),
		)
		require.ErrorIs(t, err, ErrDiskFull)
		fs.assertAllClosed(t)
	})

	t.Run("closes the segments that were loaded when a later one fails to load", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		newFailingChain(t, storage.NewOSFileSystem(false), dir, 0, 3)

		files := binFiles(t, dir)
		require.NoError(t, os.WriteFile(filepath.Join(dir, files[len(files)-1]), []byte("not a segment"), 0o600))

		fs := newFailingFS()
		_, err := NewChain(
			failureSegmentSize, 0, 0,
			NewLoader(NewFactory(failureSegmentSize, false, 0), dir, 0, fs),
		)
		require.Error(t, err)
		fs.assertAllClosed(t)
	})
}
