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
//

package segment

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/storage"
	storagemocks "stuw.net/alog/internal/storage/mocks"
	"stuw.net/alog/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestLoader_FindPaths(t *testing.T) {
	t.Parallel()

	t.Run("wraps stat errors", func(t *testing.T) {
		t.Parallel()

		fs := storagemocks.NewMockFileSystem(t)
		loader := NewLoader(NewFactory(0, false, 0), "/tmp/does-not-matter", 0, fs)

		fs.EXPECT().Stat(loader.directory).Return(nil, os.ErrPermission)

		_, err := loader.findPaths()
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to stat directory")
	})

	t.Run("returns error when path is file", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		filePath := filepath.Join(root, "not-a-dir")
		require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))

		loader := NewLoader(NewFactory(0, false, 0), filePath, 0, storage.NewOSFileSystem(false))

		_, err := loader.findPaths()
		require.Error(t, err)
		require.ErrorContains(t, err, "is not a directory")
	})

	t.Run("wraps mkdir errors", func(t *testing.T) {
		t.Parallel()

		fs := storagemocks.NewMockFileSystem(t)
		loader := NewLoader(NewFactory(0, false, 0), "/tmp/new-dir", 0, fs)

		fs.EXPECT().Stat(loader.directory).Return(nil, os.ErrNotExist)
		fs.EXPECT().MkdirAll(loader.directory, os.FileMode(0o750)).Return(os.ErrPermission)

		_, err := loader.findPaths()
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to create directory")
	})

	t.Run("propagates readdir errors", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		info, err := os.Stat(dir)
		require.NoError(t, err)

		fs := storagemocks.NewMockFileSystem(t)
		loader := NewLoader(NewFactory(0, false, 0), dir, 0, fs)

		fs.EXPECT().Stat(dir).Return(info, nil)
		fs.EXPECT().ReadDir(dir).Return(nil, os.ErrPermission)

		_, err = loader.findPaths()
		require.ErrorIs(t, err, os.ErrPermission)
	})
}

func TestLoader_CreateAndLoadCompatibility(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, segmentFileName(42))
	fs := storage.NewOSFileSystem(false)

	created, err := create(path, 42, fs, true)
	require.NoError(t, err)
	_, err = created.AppendOne([]byte("seed"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, created.Close())
	})

	require.Equal(t, uint64(42), created.StartSequence())
	require.Equal(t, uint64(43), created.NextSequence())

	loaded, err := load(path, fs, true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, loaded.Close())
	})

	require.Equal(t, int(created.StartSequence()), int(loaded.StartSequence()))
	require.Equal(t, int(created.NextSequence()), int(loaded.NextSequence()))
	_, writeErr := loaded.AppendOne([]byte("ok"))
	require.NoError(t, writeErr)
}

func TestLoader_Load(t *testing.T) {
	t.Parallel()

	t.Run("creates first segment when directory is empty", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 2, 1024)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		require.Equal(t, chain.Segments().Len(), 1)
		require.Equal(t, uint64(0), chain.Tail().StartSequence())
	})

	t.Run("ignores non-segment files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644)
		require.NoError(t, err)

		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		chain, err := NewChain(1024, 0, 2, loader)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		require.Equal(t, chain.Segments().Len(), 1)
	})

	t.Run("ignores symlinked segment files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "target.bin")
		err := os.WriteFile(target, []byte("not-a-segment"), 0o600)
		require.NoError(t, err)
		err = os.Symlink(target, filepath.Join(dir, "00000000000000000042.bin"))
		require.NoError(t, err)

		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		chain, err := NewChain(1024, 0, 2, loader)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		segments := chain.Segments().Slice()
		require.Len(t, segments, 1)
		require.Equal(t, filepath.Join(dir, segmentFileName(0)), segments[0].Name())
	})

	t.Run("returns error when segments exceed max", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		s0 := CreateSegmentAt(t, dir, 0)
		_, err := s0.AppendOne([]byte("a"))
		require.NoError(t, err)
		require.NoError(t, s0.Close())

		s1 := CreateSegmentAt(t, dir, 1)
		require.NoError(t, s1.Close())

		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		_, err = NewChain(1024, 0, 1, loader)
		require.Error(t, err)
	})

	t.Run("creates missing directory for single segment mode", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "nested", "missing")
		loader := NewLoader(NewFactory(0, false, 0), dir, 0, nil)
		chain, err := NewChain(1024, 0, 1, loader)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		require.Equal(t, chain.Segments().Len(), 1)
	})

	t.Run("returns out of bounds when seq beyond tail", func(t *testing.T) {
		t.Parallel()

		chain := NewLoadedChain(t, 3, 256)
		t.Cleanup(func() { require.NoError(t, chain.Close()) })

		_, err := chain.AppendBatch(
			[]frame.MultiPartPayload{
				frame.NewPayload([]byte("a")),
				frame.NewPayload([]byte("b")),
			},
		)
		require.NoError(t, err)

		err = chain.TruncateBefore(chain.Tail().NextSequence() + 1)
		require.ErrorIs(t, err, ErrOutOfBounds)
	})

	t.Run("torn tail preserves valid prefix", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), segmentFileName(42))

		s, err := create(path, 42, nil, false)
		require.NoError(t, err)

		expected := []frame.MultiPartPayload{
			frame.NewPayload([]byte("first")),
			frame.NewPayload([]byte("second")),
		}

		_, err = s.AppendBatch(expected)
		require.NoError(t, err)
		require.NoError(t, s.Close())

		appendTornFrame(t, path, 1, frame.NewPayload([]byte("third")), frame.HeaderSize+2)

		loaded, err := load(path, nil, false)
		require.NoError(t, err)
		require.NotNil(t, loaded)

		require.NoError(t, loaded.Sync())
		require.NoError(t, loaded.Close())

		reloaded, err := load(path, nil, false)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, reloaded.Close()) })

		require.Equal(t, 44, int(reloaded.NextSequence()))

		sc, err := reloaded.NewScanner(1<<20, true)
		require.NoError(t, err)

		for i, expectedPayload := range expected {
			require.True(t, sc.Scan())
			require.Equal(t, uint32(i), sc.LastIndex)
			require.Equal(t, expectedPayload.Bytes(), sc.Frame.Payload)
		}

		require.False(t, sc.Scan())
		require.ErrorIs(t, sc.Err(), io.EOF)
	})
}

// FuzzSegment_LoadFromBytes loads arbitrary segment bytes. Most random inputs
// should fail due to header/frame validation; on rare successful loads, every
// scanned frame must still validate and sequence numbers must remain contiguous.
func FuzzSegment_LoadFromBytes(f *testing.F) {
	f.Add([]byte{})
	seedHeader := header.NewHeader(0, 0)
	f.Add(seedHeader[:])

	f.Fuzz(func(t *testing.T, raw []byte) {
		// bound arbitrary segment bytes to keep fuzz cases memory-safe
		raw = testutil.BoundedFuzzBytes(raw, 1<<20, uint32(len(raw)))

		path := filepath.Join(t.TempDir(), "fuzz-segment.bin")
		require.NoError(t, os.WriteFile(path, raw, 0o600))

		// loading most likely fails
		s, err := load(path, nil, false)
		if err != nil {
			require.True(t,
				errors.Is(err, io.EOF) ||
					errors.Is(err, io.ErrUnexpectedEOF) ||
					errors.Is(err, header.ErrInvalidSegmentHeader) ||
					errors.Is(err, frame.ErrInvalidChecksum) ||
					errors.Is(err, frame.ErrInvalidFrameHeader) ||
					errors.Is(err, frame.ErrInvalidFrameIndex),
			)
			if s != nil {
				require.NoError(t, s.Close())
			}
			return
		}

		t.Cleanup(func() { require.NoError(t, s.Close()) })

		// this is extremely unlikely, but if load succeeds then internal
		// validation must also accept the readable frame window.
		require.NoError(t, s.init())
	})
}

func TestLoader_Create(t *testing.T) {
	t.Parallel()

	t.Run("with Load round trip", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), segmentFileName(42))
		s, err := create(path, 42, nil, false)
		require.NoError(t, err)

		_, err = s.AppendBatch([]frame.MultiPartPayload{
			frame.NewPayload([]byte("a")),
			frame.NewPayload([]byte("bb")),
		})
		require.NoError(t, err)
		require.Equal(t, uint64(44), s.NextSequence())
		require.NoError(t, s.Close())

		loaded, err := load(path, nil, false)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, loaded.Close()) })

		require.Equal(t, uint64(42), loaded.StartSequence())
		require.Equal(t, uint64(44), loaded.NextSequence())
	})
}
