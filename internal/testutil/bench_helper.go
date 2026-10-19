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

package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	envBenchQuick = "ALOG_BENCH_QUICK"
	envNoReseed   = "ALOG_NO_RESEED"
)

// ALogRepoRoot is the detected repository root for bench/test helpers.
var ALogRepoRoot string

func init() {
	var err error
	ALogRepoRoot, err = aLogRepoRoot()
	if err != nil {
		panic(fmt.Sprintf("ALogRepoRoot() error = %v", err))
	}
}

// BenchmarkPayload generates a byte slice of the specified size for benchmarking purposes.
func BenchmarkPayload(size int) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte(i)
	}
	return p
}

// ReportDomainMetrics reports the metrics for a benchmark, including operations per second,
// records per second, and megabytes per second.
func ReportDomainMetrics(b *testing.B, recordsPerOp int64, bytesPerOp int64) {
	b.Helper()
	elapsed := b.Elapsed().Seconds()
	if elapsed <= 0 {
		return
	}

	opsPerSec := float64(b.N) / elapsed
	recordsPerSec := float64(recordsPerOp) * opsPerSec
	mbPerSec := float64(bytesPerOp) * opsPerSec / (1024.0 * 1024.0)
	b.ReportMetric(recordsPerSec, "records/s")
	b.ReportMetric(mbPerSec, "MB/s")
	// if recordsPerOp > 0 {
	// 	b.ReportMetric(float64(bytesPerOp)/float64(recordsPerOp), "bytes/record")
	// }
}

// BenchmarkLabelInt generates a benchmark label string in the format "prefix=n" for reporting purposes.
func BenchmarkLabelInt(prefix string, n int) string {
	return prefix + "=" + strconv.Itoa(n)
}

// QuickBenchOnly skips a benchmark when quick mode is enabled.
func QuickBenchOnly(b *testing.B, reason string) {
	b.Helper()
	if !QuickBenchEnabled() {
		return
	}
	if reason == "" {
		reason = "slow benchmark skipped in quick mode"
	}
	b.Skip(reason)
}

// QuickBenchEnabled reports whether quick benchmark mode is enabled.
func QuickBenchEnabled() bool {
	return os.Getenv(envBenchQuick) != ""
}

// AssertBenchSeedingAllowed reports whether benchmark seeding should fail when missing.
func AssertBenchSeedingAllowed(b *testing.B) {
	if os.Getenv(envNoReseed) != "" {
		b.Fatalf("benchmark seeding is disabled by %s", envNoReseed)
	}
}

// RawFileForBench creates a temporary raw file for benchmarking purposes and returns the file handle.
func RawFileForBench(b *testing.B) *os.File {
	b.Helper()
	path := filepath.Join(b.TempDir(), "raw.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		b.Fatalf("open raw file: %v", err)
	}
	b.Cleanup(func() { _ = f.Close() })
	return f
}

// aLogRepoRoot returns the repository root by finding go.mod with module stuw.net/alog.
func aLogRepoRoot() (string, error) {
	const moduleDecl = "module stuw.net/alog"

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	dir := wd
	for {
		modPath := filepath.Join(dir, "go.mod")
		if raw, readErr := os.ReadFile(modPath); readErr == nil {
			if strings.Contains(string(raw), moduleDecl) {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("module root not found from %s", wd)
		}
		dir = parent
	}
}
