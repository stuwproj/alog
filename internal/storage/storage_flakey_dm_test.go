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

//go:build linux && flaky

package storage

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fuweid/go-dmflakey"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var enableRoot bool

func init() {
	flag.BoolVar(&enableRoot, "test.root", false, "enable tests that require root")
}

func TestOSFile_WritePartsAt(t *testing.T) {
	requireRoot(t)

	t.Run("AllowWrites", func(t *testing.T) {
		flakey, mountPoint := initFlakey(t)
		require.NoError(t, flakey.AllowWrites(
			dmflakey.WithIntervalFeatOpt(2*time.Second),
		))

		parts := makeWritePartsAtParts()
		want := flatten(parts)
		path := filepath.Join(mountPoint, "allow-writes.bin")

		fs := NewOSFileSystem(false)
		f, err := fs.Create(path, 0)
		require.NoError(t, err)

		n, err := f.WritePartsAt(parts, 0)
		require.NoError(t, err)
		require.Equal(t, len(want), n)
		require.NoError(t, f.Sync())
		require.NoError(t, f.Close())

		require.NoError(t, remount(flakey, mountPoint))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("ErrorWrites", func(t *testing.T) {
		flakey, mountPoint := initFlakey(t)
		parts := makeWritePartsAtParts()
		want := flatten(parts)
		path := filepath.Join(mountPoint, "error-writes.bin")

		fs := NewOSFileSystem(false)
		require.NoError(t, createDurableEmptyFile(fs, mountPoint, path))

		require.NoError(t, flakey.ErrorWrites(
			dmflakey.WithSyncFSFeatOpt(true),
			dmflakey.WithIntervalFeatOpt(2*time.Second),
		))

		f, err := fs.Open(path)
		require.NoError(t, err)

		n, writeErr := f.WritePartsAt(parts, 0)
		syncErr := f.Sync()
		require.NoError(t, f.Close())
		if syncErr != nil && writeErr == nil {
			writeErr = syncErr
		}

		require.Error(t, writeErr)
		require.GreaterOrEqual(t, n, 0)
		require.LessOrEqual(t, n, len(want))

		observed, err := os.ReadFile(path)
		require.NoError(t, err)
		require.LessOrEqual(t, len(observed), len(want))

		require.NoError(t, flakey.AllowWrites(
			dmflakey.WithIntervalFeatOpt(2*time.Second),
		))

		require.NoError(t, remount(flakey, mountPoint))

		retry, err := fs.Open(path)
		require.NoError(t, err)
		require.NoError(t, retry.Truncate(0))
		n2, err := retry.WritePartsAt(parts, 0)
		require.NoError(t, err)
		require.Equal(t, len(want), n2)
		require.NoError(t, retry.Sync())
		require.NoError(t, retry.Close())

		finalBytes, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want, finalBytes)
	})

	t.Run("DropWrites", func(t *testing.T) {
		flakey, mountPoint := initFlakey(t)

		fs := NewOSFileSystem(false)
		stablePath := filepath.Join(mountPoint, "stable.bin")
		droppedPath := filepath.Join(mountPoint, "dropped.bin")
		payload := []byte("this-should-drop")

		stable, err := fs.Create(stablePath, 0)
		require.NoError(t, err)
		_, err = stable.WritePartsAt([][]byte{[]byte("stable-data")}, 0)
		require.NoError(t, err)
		require.NoError(t, stable.Sync())
		require.NoError(t, stable.Close())
		require.NoError(t, fs.SyncDir(mountPoint))

		require.NoError(t, createDurableEmptyFile(fs, mountPoint, droppedPath))

		require.NoError(t, flakey.DropWrites(
			dmflakey.WithSyncFSFeatOpt(true),
			dmflakey.WithIntervalFeatOpt(2*time.Second),
		))

		dropped, err := fs.Open(droppedPath)
		require.NoError(t, err)
		require.NoError(t, dropped.Truncate(0))
		n, err := dropped.WritePartsAt([][]byte{payload}, 0)
		require.NoError(t, err)
		require.Equal(t, len(payload), n)
		require.NoError(t, dropped.Sync())
		require.NoError(t, dropped.Close())

		require.NoError(t, flakey.AllowWrites(
			dmflakey.WithIntervalFeatOpt(2*time.Second),
		))
		require.NoError(t, remount(flakey, mountPoint))

		stableBytes, err := os.ReadFile(stablePath)
		require.NoError(t, err)
		require.Equal(t, []byte("stable-data"), stableBytes)

		droppedBytes, err := os.ReadFile(droppedPath)
		require.NoError(t, err)
		require.NotEqual(t, payload, droppedBytes)
	})
}

func initFlakey(t *testing.T) (dmflakey.Flakey, string) {
	t.Helper()

	base := t.TempDir()
	mountPoint := filepath.Join(base, "mnt")
	require.NoError(t, os.MkdirAll(mountPoint, 0o700))

	name := fmt.Sprintf("alog-flakey-%d", time.Now().UnixNano())
	flakey, err := dmflakey.InitFlakey(name, base, dmflakey.FSTypeEXT4)
	require.NoError(t, err)

	require.NoError(t, unix.Mount(flakey.DevicePath(), mountPoint, string(flakey.Filesystem()), 0, "commit=1000"))

	t.Cleanup(func() {
		require.NoError(t, unmount(mountPoint))
		require.NoError(t, flakey.Teardown())
	})

	return flakey, mountPoint
}

func remount(flakey dmflakey.Flakey, mountPoint string) error {
	if err := unmount(mountPoint); err != nil {
		return err
	}

	return unix.Mount(flakey.DevicePath(), mountPoint, string(flakey.Filesystem()), 0, "")
}

func createDurableEmptyFile(fs *OSFileSystem, dir, path string) error {
	f, err := fs.Create(path, 0)
	if err != nil {
		return err
	}

	if err = f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}

	if err = f.Close(); err != nil {
		return err
	}

	return fs.SyncDir(dir)
}

func makeWritePartsAtParts() [][]byte {
	parts := make([][]byte, 0, 1300)
	for i := range 1300 {
		switch {
		case i%13 == 0:
			parts = append(parts, nil)
		case i%7 == 0:
			parts = append(parts, []byte{})
		default:
			parts = append(parts, []byte{byte(i), byte(i >> 1), byte(i >> 2)})
		}
	}

	return parts
}

func flatten(parts [][]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}

	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}

	return out
}

func unmount(target string) error {
	for range 50 {
		err := unix.Unmount(target, 0)
		if err == nil || err == unix.EINVAL {
			return nil
		}

		if err != unix.EBUSY {
			return err
		}

		time.Sleep(100 * time.Millisecond)
	}

	return unix.EBUSY
}

func requireRoot(t *testing.T) {
	t.Helper()

	if !enableRoot {
		t.Fatalf("requires root setup; re-run with -test.root")
	}

	if os.Geteuid() != 0 {
		t.Fatalf("requires root privileges")
	}
}
