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
	"fmt"
	"os"
	"path/filepath"

	"stuw.net/alog/internal/checkpoint"
	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/storage"
)

const segmentFileExt = ".bin"

func splitMix64(v uint64) uint64 {
	v += 0x9e3779b97f4a7c15
	v = (v ^ (v >> 30)) * 0xbf58476d1ce4e5b9
	v = (v ^ (v >> 27)) * 0x94d049bb133111eb
	return v ^ (v >> 31)
}

// segmentFileName deterministically generates a segment file name based on
// the start sequence number. the start sequence number is intentionally obfuscated
// using the splitMix64 function to the casual observer from inferring that the
// segment file name is based on the start sequence number. The actual start sequence
// number is stored in the segment header and can be mutated.
func segmentFileName(startSeq uint64) string {
	return fmt.Sprintf("%016x%s", splitMix64(startSeq), segmentFileExt)
}

// Loader is responsible for loading segments from disk and creating new segments when necessary.
type Loader struct {
	fs            storage.FileSystem
	factory       *Factory
	directory     string
	startSequence uint64
}

// NewLoader creates a new Loader instance with the given parameters.
func NewLoader(
	factory *Factory,
	directory string,
	startSequence uint64,
	fs storage.FileSystem,
) *Loader {
	return &Loader{
		factory:       factory,
		directory:     directory,
		startSequence: startSequence,
		fs:            fs,
	}
}

// ProjectDiskSize returns the disk size of a new segment file,
// which is determined by the segment factory's prealloc configuration,
// and the size of the segment header.
func (l *Loader) ProjectDiskSize(batch []frame.MultiPartPayload) int64 {
	return max(frame.BatchSize(batch), l.factory.PreallocateSize()) + header.HeaderSize
}

// Load loads a segment from the given path and returns a Segment instance.
func (l *Loader) Load() (segments []*Segment, err error) {
	l.ensureFileSystem()

	paths, err := l.findPaths()
	if err != nil {
		return nil, err
	}

	var segment *Segment

	if len(paths) == 0 {
		segment, err = l.Create(l.startSequence)
		if err != nil {
			return nil, err
		}

		segments = append(segments, segment)

		return segments, nil
	}

	for _, path := range paths {
		segment, err = l.load(path)
		if err != nil {
			return segments, err
		}

		segments = append(segments, segment)
	}

	return segments, nil
}

// Create a new empty segment file.
// This also syncs the parent directory.
func (l *Loader) Create(startSequence uint64) (*Segment, error) {
	l.ensureFileSystem()

	if l.factory == nil {
		return nil, errors.New("segment factory is required")
	}

	path := filepath.Join(l.directory, segmentFileName(startSequence))

	hdr := header.NewHeader(startSequence, 0)

	file, err := l.fs.Create(path, l.factory.prealloc)
	if err != nil {
		return nil, err
	}

	// A file that is not completely set up is removed. Loading it again would count it against
	// the disk budget, and it is not a valid segment.
	abandon := func(err error) (*Segment, error) {
		return nil, errors.Join(err, file.Close(), l.fs.Remove(path))
	}

	_, err = file.Write(hdr[:])
	if err != nil {
		return abandon(err)
	}

	// TODO: optimize using vector since we're now doing 2 writes
	fhdr := l.emptyEOF(0)
	_, err = file.Write(fhdr[:])
	if err != nil {
		return abandon(err)
	}

	if err = file.Sync(); err != nil {
		return abandon(err)
	}

	if syncErr := l.fs.SyncDir(filepath.Dir(path)); syncErr != nil {
		return abandon(syncErr)
	}

	meta, err := hdr.Meta()
	if err != nil {
		return abandon(err)
	}

	segment := l.factory.New(file, meta, header.HeaderSize)

	return segment, nil
}

// Recycle reuses an existing segment file by overwriting its header with a new start sequence number.
// The frame generation is bumped, and the slot generation continues from the file's current metadata.
func (l *Loader) Recycle(path string, startSequence uint64) (segment *Segment, err error) {
	l.ensureFileSystem()

	if l.factory == nil {
		return nil, errors.New("segment factory is required")
	}

	file, err := l.fs.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
		}
	}()

	var current header.Header
	if _, err = file.ReadAt(current[:], 0); err != nil {
		return nil, err
	}

	if !current.Valid() {
		return nil, header.ErrInvalidSegmentHeader
	}

	meta, err := current.Meta()
	if err != nil {
		return nil, err
	}

	generation := meta.FrameGeneration() + 1

	// write the new metadata
	if err := header.RecycleFile(
		file,
		header.RecycleUpdate{
			Seq:             startSequence,
			FrameGeneration: generation,
			Flags:           header.FlagCheckpointStateInitialized,
			CPRemaining:     l.factory.checkpointStride,
		},
		&meta,
	); err != nil {
		return nil, err
	}

	// terminate the (now empty) segment so frames of the previous generation
	// are never interpreted as part of it
	eof := l.emptyEOF(generation)
	if _, err := file.WriteAt(eof[:], header.HeaderSize); err != nil {
		return nil, err
	}

	if err := file.Sync(); err != nil {
		return nil, err
	}

	// create a new segment instance with the recycled file
	segment = l.factory.New(file, meta, 0)

	return segment, nil
}

// Load loads a segment from the given path and returns a Segment instance.
func (l *Loader) load(path string) (*Segment, error) {
	l.ensureFileSystem()

	file, err := l.fs.Open(path)
	if err != nil {
		return nil, err
	}

	segment, err := l.loadFrom(file)
	if err != nil {
		return nil, err
	}

	return segment, nil
}

// loadFrom creates the segment of an open file. When that fails the file is closed.
func (l *Loader) loadFrom(file storage.File) (segment *Segment, err error) {
	defer func() {
		if err != nil {
			// the segment closes the mapping that init opened, and the file
			if segment != nil {
				err = errors.Join(err, segment.Close())
			} else {
				err = errors.Join(err, file.Close())
			}
		}
	}()

	var h header.Header
	_, err = file.ReadFull(h[:])
	if err != nil {
		return nil, err
	}

	if !h.Valid() {
		return nil, header.ErrInvalidSegmentHeader
	}

	meta, err := h.Meta()
	if err != nil {
		return nil, err
	}

	startOffset := meta.ReadOffset()

	metaOffset := max(startOffset, int64(header.HeaderSize))
	if l.factory == nil {
		return nil, errors.New("segment factory is required")
	}

	segment = l.factory.New(file, meta, metaOffset)

	if err = segment.init(); err != nil {
		return segment, err
	}

	return segment, nil
}

// emptyEOF returns the EOF checkpoint of an empty segment.
func (l *Loader) emptyEOF(generation uint16) frame.Header {
	stride := l.factory.checkpointStride

	return checkpoint.NewEOF(generation, stride, checkpoint.State{Remaining: stride})
}

func (l *Loader) findPaths() ([]string, error) {
	var files []os.DirEntry
	stat, err := l.fs.Stat(l.directory)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat directory %s: %w", l.directory, err)
		}

		if err = l.fs.MkdirAll(l.directory, 0o750); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", l.directory, err)
		}
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("path %s is not a directory", l.directory)
	}

	files, err = l.fs.ReadDir(l.directory)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(files))
	dirPrefix := l.directory + string(filepath.Separator)

	for _, file := range files {
		if file.IsDir() || file.Type()&os.ModeSymlink != 0 || filepath.Ext(file.Name()) != segmentFileExt {
			continue
		}

		filePath := dirPrefix + file.Name()
		paths = append(paths, filePath)
	}

	return paths, nil
}

func (l *Loader) ensureFileSystem() {
	if l.fs == nil {
		l.fs = storage.NewOSFileSystem(false)
	}
}
