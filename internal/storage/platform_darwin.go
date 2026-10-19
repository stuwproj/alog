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

//go:build darwin

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// syncFile flushes the file to stable storage.
//
// On macOS this is F_FULLFSYNC, which is what os.File.Sync does there (and it
// falls back to fsync where F_FULLFSYNC is not supported). A plain fsync does
// not flush the drive cache, so it would not be durable. F_FULLFSYNC is a lot
// slower than fdatasync on Linux.
func syncFile(f *os.File) error {
	return f.Sync()
}

// preallocate allocates size bytes for the file, and sets its length to size,
// like fallocate does on Linux.
//
// macOS has no fallocate. F_PREALLOCATE reserves the space but leaves the length
// alone, so the length is set afterwards. Not every filesystem supports
// F_PREALLOCATE. Where it is not supported the file is only extended, which means
// no space is reserved.
func preallocate(f *os.File, size int64) error {
	fd := f.Fd()

	store := unix.Fstore_t{
		Flags:   unix.F_ALLOCATECONTIG,
		Posmode: unix.F_PEOFPOSMODE,
		Length:  size,
	}

	err := unix.FcntlFstore(fd, unix.F_PREALLOCATE, &store)
	if err != nil {
		// contiguous space is not required, take what there is
		store.Flags = unix.F_ALLOCATEALL
		err = unix.FcntlFstore(fd, unix.F_PREALLOCATE, &store)
	}

	if isOutOfSpace(err) {
		return err
	}

	return f.Truncate(size)
}

// isOutOfSpace reports whether err means the space really is not available, as
// opposed to the filesystem not supporting preallocation.
func isOutOfSpace(err error) bool {
	return errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT) || errors.Is(err, unix.EFBIG)
}
