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
	"fmt"
	"io"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

const maxIOV = 1024

var (
	// ErrReadOnly reports that alog rejected a write in read-only mode.
	ErrReadOnly = errors.New("alog: read-only mode")
)

func readOnlyError() error {
	return errors.Join(ErrReadOnly, os.ErrPermission)
}

// FileSystem abstracts filesystem operations used by alog packages.
type FileSystem interface {
	Open(name string) (File, error)
	Create(name string, size int64) (File, error)
	Stat(string) (os.FileInfo, error)
	MkdirAll(string, os.FileMode) error
	ReadDir(string) ([]os.DirEntry, error)
	Remove(string) error
	SyncDir(string) error
}

// File is an abstract open file handle used by segment and frame code.
type File interface {
	Name() string
	Fd() uintptr
	Stat() (os.FileInfo, error)
	Read([]byte) (int, error)
	ReadAt([]byte, int64) (int, error)
	ReadFull([]byte) (int, error)
	Write([]byte) (int, error)
	WriteAt([]byte, int64) (int, error)
	WritePartsAt([][]byte, int64) (int, error)
	Truncate(int64) error
	Seek(int64, int) (int64, error)
	Sync() error
	Close() error
}

var (
	_ File       = (*OSFile)(nil)
	_ FileSystem = (*OSFileSystem)(nil)
)

// OSFile adapts *os.File to the File interface.
type OSFile struct {
	f        *os.File
	parts    [][]byte
	readOnly bool
}

// Close implements [File].
func (f *OSFile) Close() error {
	return f.f.Close()
}

// Name implements [File].
func (f *OSFile) Name() string {
	return f.f.Name()
}

// ReadAt implements [File].
func (f *OSFile) ReadAt(b []byte, off int64) (int, error) {
	return f.f.ReadAt(b, off)
}

// Read implements [File].
func (f *OSFile) Read(b []byte) (int, error) {
	return f.f.Read(b)
}

// ReadFull implements [File].
func (f *OSFile) ReadFull(b []byte) (int, error) {
	return io.ReadFull(f.f, b)
}

// Seek implements [File].
func (f *OSFile) Seek(offset int64, whence int) (int64, error) {
	return f.f.Seek(offset, whence)
}

// Sync implements [File].
func (f *OSFile) Sync() error {
	if f.readOnly {
		return readOnlyError()
	}

	return syncFile(f.f)
}

// Truncate implements [File].
func (f *OSFile) Truncate(size int64) error {
	if f.readOnly {
		return readOnlyError()
	}

	return f.f.Truncate(size)
}

// Write implements [File].
func (f *OSFile) Write(b []byte) (int, error) {
	if f.readOnly {
		return 0, readOnlyError()
	}

	return f.f.Write(b)
}

// WriteAt implements [File].
func (f *OSFile) WriteAt(b []byte, off int64) (int, error) {
	if f.readOnly {
		return 0, readOnlyError()
	}

	return f.f.WriteAt(b, off)
}

// WritePartsAt implements [File].
func (f *OSFile) WritePartsAt(parts [][]byte, off int64) (int, error) {
	if f.readOnly {
		return 0, readOnlyError()
	}

	fd := int(f.f.Fd())
	partIdx := 0
	partOff := 0
	total := 0

	for partIdx < len(parts) {
		remainingCount := len(parts) - partIdx
		chunkCount := min(remainingCount, maxIOV)

		if cap(f.parts) < chunkCount {
			f.parts = make([][]byte, chunkCount)
		} else {
			f.parts = f.parts[:chunkCount]
		}

		partCount := 0
		chunkBytes := 0

		for i := range chunkCount {
			part := parts[partIdx+i]
			if i == 0 && partOff > 0 {
				part = part[partOff:]
			}

			if len(part) == 0 {
				continue
			}

			f.parts[partCount] = part
			partCount++
			chunkBytes += len(part)
		}

		if chunkBytes == 0 {
			partIdx += chunkCount
			partOff = 0
			continue
		}

		n, err := unix.Pwritev(fd, f.parts[:partCount], off)
		if err == unix.EINTR {
			continue
		}

		if err != nil {
			return total, err
		}

		if n == 0 {
			return total, io.ErrShortWrite
		}

		if n > math.MaxInt {
			return 0, fmt.Errorf("write size exceeds int: %d", n)
		}

		written := n
		total += written
		off += int64(written)

		for partIdx < len(parts) {
			partLen := len(parts[partIdx]) - partOff
			if partLen <= 0 {
				partIdx++
				partOff = 0
				continue
			}

			if written >= partLen {
				written -= partLen
				partIdx++
				partOff = 0
				if written == 0 {
					break
				}

				continue
			}

			partOff += written
			break
		}
	}

	return total, nil
}

// Stat returns metadata for the underlying file.
func (f *OSFile) Stat() (os.FileInfo, error) {
	return f.f.Stat()
}

// Fd returns the underlying file descriptor for the file.
func (f *OSFile) Fd() uintptr {
	return f.f.Fd()
}

// OSFileSystem is a production FileSystem implementation backed by os package calls.
type OSFileSystem struct {
	readOnly bool
}

// NewOSFileSystem returns a filesystem implementation with optional read-only behavior.
func NewOSFileSystem(readOnly bool) *OSFileSystem {
	return &OSFileSystem{readOnly: readOnly}
}

// MkdirAll implements [FileSystem].
func (fs *OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	if fs.readOnly {
		return readOnlyError()
	}

	return os.MkdirAll(path, perm)
}

// ReadDir implements [FileSystem].
func (fs *OSFileSystem) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// Remove implements [FileSystem].
func (fs *OSFileSystem) Remove(path string) error {
	if fs.readOnly {
		return readOnlyError()
	}

	return os.Remove(path)
}

// Stat implements [FileSystem].
func (fs *OSFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// SyncDir implements [FileSystem].
func (fs *OSFileSystem) SyncDir(path string) error {
	if fs.readOnly {
		return readOnlyError()
	}

	// #nosec G304 -- path comes from controlled storage root selection.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}

	if err = dir.Sync(); err != nil {
		return errors.Join(err, dir.Close())
	}

	return dir.Close()
}

// Open opens an existing segment file in read-write mode.
func (fs *OSFileSystem) Open(name string) (File, error) {
	flags := os.O_RDWR
	if fs.readOnly {
		flags = os.O_RDONLY
	}

	f, err := os.OpenFile(name, flags, 0o600) // #nosec G304 -- storage root is caller controlled.
	if err != nil {
		return nil, err
	}

	return &OSFile{f: f, readOnly: fs.readOnly}, nil
}

// Create creates a new segment file using exclusive create semantics.
func (fs *OSFileSystem) Create(name string, size int64) (File, error) {
	if fs.readOnly {
		return nil, readOnlyError()
	}

	// #nosec G304 -- storage root is caller controlled.
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if size > 0 {
		// preallocate
		if err = preallocate(f, size); err != nil {
			return nil, errors.Join(err, f.Close())
		}
	}

	return &OSFile{f: f, readOnly: fs.readOnly}, nil
}
