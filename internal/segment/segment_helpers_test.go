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
	"os"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/storage"
	"github.com/stretchr/testify/require"
)

func NewLoadedChain(t *testing.T, maxSegments int, maxSegmentSize int64) *Chain {
	t.Helper()
	return NewLoadedChainWithDiskBudget(t, maxSegments, maxSegmentSize, 0)
}

func NewLoadedChainWithDiskBudgetAndPrealloc(t *testing.T, prealloc int64, maxSegments int, maxSegmentSize int64, maxDiskSize int64) *Chain {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))

	factory := NewFactory(prealloc, false, 0)

	return NewLoadedChainWithDiskBudgetAndFactory(t, factory, maxSegments, maxSegmentSize, maxDiskSize)
}

func NewLoadedChainWithDiskBudgetAndFactory(t *testing.T, factory *Factory, maxSegments int, maxSegmentSize int64, maxDiskSize int64) *Chain {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))

	loader := NewLoader(factory, dir, 0, nil)
	chain, err := NewChain(maxSegmentSize, maxDiskSize, maxSegments, loader)
	require.NoError(t, err)

	return chain
}

func NewLoadedChainWithDiskBudget(t *testing.T, maxSegments int, maxSegmentSize int64, maxDiskSize int64) *Chain {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))

	factory := NewFactory(0, false, 0)

	return NewLoadedChainWithDiskBudgetAndFactory(t, factory, maxSegments, maxSegmentSize, maxDiskSize)
}

func CreateSegmentAt(t *testing.T, dir string, startSeq uint64) *Segment {
	t.Helper()

	loader := NewLoader(NewFactory(0, false, 0), dir, startSeq, nil)
	s, err := loader.Create(startSeq)
	require.NoError(t, err)

	return s
}

func create(
	path string,
	startSeq uint64,
	fs storage.FileSystem,
	syncOnAppend bool,
) (*Segment, error) {
	factory := NewFactory(0, syncOnAppend, 0)
	loader := NewLoader(factory, filepath.Dir(path), startSeq, fs)

	return loader.Create(startSeq)
}

func load(
	path string,
	fs storage.FileSystem,
	syncOnAppend bool,
) (*Segment, error) {
	factory := NewFactory(0, syncOnAppend, 0)
	loader := NewLoader(factory, filepath.Dir(path), 0, fs)

	segments, err := loader.Load()
	if len(segments) == 0 {
		if err != nil {
			return nil, err
		}

		return nil, os.ErrNotExist
	}

	return segments[0], err
}
