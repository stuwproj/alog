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

//go:build linux || darwin

package alog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lockFileName is the file in the directory of a log that holds its lock. Only .bin files are
// loaded as segments, so it is not mistaken for one.
const lockFileName = "LOCK"

// lockDirectory takes an exclusive lock on the directory of a log, and creates the directory
// if there is none. The lock belongs to the open file, so it is released when the file is closed,
// and by the system when the process ends, also when it crashes.
func lockDirectory(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	//nolint:gosec // the directory is the one the caller asked the log to use
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeErr := f.Close()

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("%w: %s", ErrLocked, dir), closeErr)
		}

		return nil, errors.Join(fmt.Errorf("failed to lock %s: %w", dir, err), closeErr)
	}

	return f, nil
}
