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

package alog

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/testutil"
)

var (
	seedBenchRootDir        = path.Join("testdata", "bench", "logs")
	defaultBenchTempRootDir = path.Join("/tmp", "alog", "logs")
)

const scratchSeedThresholdRecords = 8

const benchLogLayoutVersion = 4

// BenchLogMeta holds benchmark log characteristics and options.
type BenchLogMeta struct {
	Options Options
	Seed    BenchLogSeed
}

// BenchLogSeed describes a benchmark seed dataset.
type BenchLogSeed struct {
	Count       int
	PayloadSize int
}

type benchLogDiskKey struct {
	Seed           BenchLogSeed
	Storage        StorageOptions
	HeaderSize     int
	SegmentVersion uint32
	LayoutVersion  int
}

// GetReadOnlyBenchLog returns a read-only log for benchmarking.
// Seeded logs are loaded from testdata; unseeded logs use an empty scratch log.
func GetReadOnlyBenchLog(b *testing.B, meta BenchLogMeta) *Log {
	b.Helper()

	if !hasBenchSeed(meta) {
		meta.Options.Storage.ReadOnly = true
		return GetLog(b, scratchLogDir(b), meta)
	}

	meta.Options.Storage.ReadOnly = false
	dir := SeedWritableBenchLog(b, meta)
	meta.Options.Storage.ReadOnly = true

	return GetLog(b, dir, meta)
}

func GetLog(b *testing.B, dir string, meta BenchLogMeta) *Log {
	b.Helper()

	l, err := New(dir, meta.Options)
	if err != nil {
		b.Fatalf("NewLog() error = %v", err)
	}

	b.Cleanup(func() { _ = l.Close() })

	return l
}

func marshalBenchLogMeta(b *testing.B, meta BenchLogMeta) []byte {
	opts := normalizeOptions(meta.Options)

	data, err := json.MarshalIndent(benchLogDiskKey{
		Seed:           meta.Seed,
		HeaderSize:     header.HeaderSize,
		SegmentVersion: header.SegmentVersion,
		LayoutVersion:  benchLogLayoutVersion,
		Storage: StorageOptions{
			CheckpointStride: opts.Storage.CheckpointStride,
			StartSequence:    opts.Storage.StartSequence,
			MaxDiskSize:      opts.Storage.MaxDiskSize,
			MaxSegments:      opts.Storage.MaxSegments,
			MaxSegmentSize:   opts.Storage.MaxSegmentSize,
			Preallocate:      opts.Storage.Preallocate,
		},
	}, "", "  ")
	if err != nil {
		b.Fatalf("failed to marshal meta: %v", err)
	}

	return data
}

func seedLogDir(metaData []byte) string {
	return filepath.Join(seedBenchRootDir, fmt.Sprintf("%08x", crc32.ChecksumIEEE(metaData)))
}

// SeedWritableBenchLog ensures a seeded dataset exists in testdata/logs.
func SeedWritableBenchLog(b *testing.B, meta BenchLogMeta) string {
	b.Helper()
	testutil.AssertBenchSeedingAllowed(b)

	if !hasBenchSeed(meta) {
		b.Fatalf("SeedWritableBenchLog requires non-empty meta.Seed")
	}

	data := marshalBenchLogMeta(b, meta)
	dir := seedLogDir(data)

	if isDir(dir) {
		return dir
	}

	b.Logf(
		"seeding benchmark log to %s with %d records of %d bytes",
		dir,
		meta.Seed.Count,
		meta.Seed.PayloadSize,
	)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("MkdirAll() error = %v", err)
	}

	// write meta.json to rootDir for debugging purposes
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		b.Fatalf("failed to write meta.json: %v", err)
	}

	l, err := New(dir, meta.Options)
	if err != nil {
		b.Fatalf("NewLog() error = %v", err)
	}

	p := testutil.BenchmarkPayload(meta.Seed.PayloadSize)
	for range meta.Seed.Count {
		if _, err = l.Append(p); err != nil {
			_ = l.Close()
			b.Fatalf("Log.Append seed error = %v", err)
		}
	}
	if err = l.Sync(); err != nil {
		_ = l.Close()
		b.Fatalf("Log.Sync seed error = %v", err)
	}
	if err = l.Close(); err != nil {
		b.Fatalf("Log.Close() error = %v", err)
	}

	return dir
}

func scratchLogDir(b *testing.B) string {
	b.Helper()
	return path.Join(defaultBenchTempRootDir, b.Name())
}

// GetWritableBenchLog returns a writable benchmark log.
// Seeded meta returns a tmp copy of testdata dataset; unseeded meta returns scratch tmp log.
func GetWritableBenchLog(b *testing.B, meta BenchLogMeta) *Log {
	b.Helper()

	if useScratchSeed(meta) || !hasBenchSeed(meta) {
		return scratchWritableLog(b, meta)
	}

	return tmpLogCopy(b, meta)
}

func hasBenchSeed(meta BenchLogMeta) bool {
	return meta.Seed.Count > 0
}

func useScratchSeed(meta BenchLogMeta) bool {
	return meta.Seed.Count <= scratchSeedThresholdRecords
}

func scratchWritableLog(b *testing.B, meta BenchLogMeta) *Log {
	b.Helper()

	dir := scratchLogDir(b)
	if err := os.RemoveAll(dir); err != nil {
		b.Fatalf("RemoveAll() error = %v", err)
	}

	l, err := New(dir, meta.Options)
	if err != nil {
		b.Fatalf("NewLog() error = %v", err)
	}

	if !hasBenchSeed(meta) {
		return l
	}

	p := testutil.BenchmarkPayload(meta.Seed.PayloadSize)
	for range meta.Seed.Count {
		if _, err = l.Append(p); err != nil {
			_ = l.Close()
			b.Fatalf("Log.Append seed error = %v", err)
		}
	}
	if err = l.Sync(); err != nil {
		_ = l.Close()
		b.Fatalf("Log.Sync seed error = %v", err)
	}

	return l
}

func tmpLogCopy(b *testing.B, meta BenchLogMeta) *Log {
	b.Helper()

	dstDir := scratchLogDir(b)
	srcDir := seedLogDir(marshalBenchLogMeta(b, meta))
	if !isDir(srcDir) {
		SeedWritableBenchLog(b, meta)
	}

	if err := os.RemoveAll(dstDir); err != nil {
		b.Fatalf("failed to clean tmp log directory: %v", err)
	}

	if err := copyDirTree(srcDir, dstDir); err != nil {
		b.Fatalf("failed to copy log directory: %v", err)
	}

	l, err := New(dstDir, meta.Options)
	if err != nil {
		b.Fatalf("NewLog() error = %v", err)
	}
	return l
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}

	return st.IsDir()
}

func copyDirTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	copyFile := func(src, dst string) error {
		var in, out *os.File

		in, err = os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close() //nolint:errcheck

		out, err = os.OpenFile(dst, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer out.Close() //nolint:errcheck

		if _, err = io.Copy(out, in); err != nil {
			return err
		}

		return out.Sync()
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err = copyDirTree(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		if err = copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}
