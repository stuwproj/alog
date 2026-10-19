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

package alog

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/header"
	"stuw.net/alog/internal/testutil"
)

func BenchmarkLog_Append(b *testing.B) {
	baselines := benchmarkAppendRawBaselines(b)

	sizes := []struct {
		name string
		size int
	}{{"0B", 0}, {"16B", 16}, {"256B", 256}, {"4K", 4 << 10}}

	for _, c := range sizes {
		b.Run("direct/"+c.name, func(b *testing.B) {
			if c.size >= 4<<10 {
				testutil.QuickBenchOnly(b, "skip sync benchmark for payload in quick mode")
			}

			maxSeg := int64(64 * 1024 * 1024)
			if c.size >= 1<<20 {
				maxSeg = 256 * 1024 * 1024
			}

			l := GetWritableBenchLog(b, benchMeta(4, maxSeg))
			payload := testutil.BenchmarkPayload(c.size)
			rawMBps := baselines.write[len(payload)]

			for b.Loop() {
				if _, err := l.Append(payload); err != nil {
					b.Fatalf("Log.Append() error = %v", err)
				}
			}

			reportAppendMetrics(b, 1, int64(len(payload)), rawMBps)
		})
	}

	for _, c := range sizes {
		b.Run("sync/"+c.name, func(b *testing.B) {
			if c.size >= 4<<10 {
				testutil.QuickBenchOnly(b, "skip sync benchmark for payload in quick mode")
			}

			m := benchMeta(4, 256*1024*1024)
			m.Options.Storage.SyncOnAppend = true

			l := GetWritableBenchLog(b, m)
			payload := MultipartPayload{testutil.BenchmarkPayload(c.size)}
			rawMBps := baselines.sync[len(payload[0])]

			for b.Loop() {
				if _, err := l.AppendMultipart(payload); err != nil {
					b.Fatalf("Log.Append() error = %v", err)
				}
			}

			reportAppendMetrics(b, 1, int64(len(payload[0])), rawMBps)
		})
	}

	for _, c := range sizes {
		b.Run("sync/batch10/"+c.name, func(b *testing.B) {
			m := benchMeta(4, 256*1024*1024)
			m.Options.Storage.SyncOnAppend = true

			l := GetWritableBenchLog(b, m)
			kind := []byte{0} // matches alog-fsm user-record envelope kind

			payload := testutil.BenchmarkPayload(c.size)
			record := MultipartPayload{kind, payload}

			bytesPerOp := (int64(len(kind)+len(payload)) * 10) + (10 * 16) // 10 records, each with 16-byte header
			recordsPerOp := int64(10)

			payloads := make([]MultipartPayload, 10)
			for i := range payloads {
				payloads[i] = record
			}

			for b.Loop() {
				if _, err := l.AppendMultipartBatch(payloads); err != nil {
					b.Fatalf("Log.AppendMultipartBatch() error = %v", err)
				}
			}

			reportAppendMetrics(b, recordsPerOp, bytesPerOp, 0)
		})
	}

	b.Run("rotate/append-boundary", func(b *testing.B) {
		payload := testutil.BenchmarkPayload(256)
		rawMBps := baselines.write[len(payload)]
		maxSeg := int64(header.HeaderSize + (2 * frame.HeaderSize) + len(payload))
		l := GetWritableBenchLog(b, benchMeta(0, maxSeg))
		if _, err := l.Append(payload); err != nil {
			b.Fatalf("seed append error = %v", err)
		}

		for b.Loop() {
			if _, err := l.Append(payload); err != nil {
				b.Fatalf("Log.Append() error = %v", err)
			}
		}

		reportAppendMetrics(b, 1, int64(len(payload)), rawMBps)
	})

	b.Run("rotate/reap-boundary", func(b *testing.B) {
		payload := testutil.BenchmarkPayload(256)
		rawMBps := baselines.write[len(payload)]

		l := GetWritableBenchLog(b, benchMeta(2, int64(header.HeaderSize+(2*frame.HeaderSize)+len(payload))))
		if _, err := l.Append(payload); err != nil {
			b.Fatalf("seed append error = %v", err)
		}

		for b.Loop() {
			if _, err := l.Append(payload); err != nil {
				b.Fatalf("Log.Append() error = %v", err)
			}
		}

		reportAppendMetrics(b, 1, int64(len(payload)), rawMBps)
	})

	b.Run("rotate/frequent", func(b *testing.B) {
		p := testutil.BenchmarkPayload(64)
		rawMBps := baselines.write[len(p)]
		l := GetWritableBenchLog(b, benchMeta(0, int64(64+8*(16+len(p)))))

		for b.Loop() {
			if _, err := l.Append(p); err != nil {
				b.Fatalf("Log.Append() error = %v", err)
			}
		}

		reportAppendMetrics(b, 1, int64(len(p)), rawMBps)
	})
}

func BenchmarkLog_AppendOne(b *testing.B) {
	payload := testutil.BenchmarkPayload(256)
	l := GetWritableBenchLog(b, benchMeta(4, 64*1024*1024))

	for b.Loop() {
		if _, err := l.AppendOne(payload); err != nil {
			b.Fatalf("Log.AppendOne() error = %v", err)
		}
	}

	reportAppendMetrics(b, 1, int64(len(payload)), 0)
}

func BenchmarkLog_AppendMultipart(b *testing.B) {
	payload := testutil.BenchmarkPayload(256)
	half := len(payload) / 2
	record := MultipartPayload{payload[:half], payload[half:]}

	for _, records := range []int{1, 8, 64} {
		b.Run(testutil.BenchmarkLabelInt("records", records), func(b *testing.B) {
			if records >= 64 {
				testutil.QuickBenchOnly(b, "skip large multipart batch benchmark in quick mode")
			}

			l := GetWritableBenchLog(b, benchMeta(4, 64*1024*1024))
			batch := make([]MultipartPayload, records)
			for i := range batch {
				batch[i] = record
			}

			b.SetBytes(int64(records * len(payload)))
			b.ResetTimer()
			for b.Loop() {
				if _, err := l.AppendMultipartBatch(batch); err != nil {
					b.Fatalf("Log.AppendMultipartBatch() error = %v", err)
				}
			}

			reportAppendMetrics(b, int64(records), int64(records*len(payload)), 0)
		})
	}
}

func BenchmarkLog_Load(b *testing.B) {

	b.Run("records=100K", func(b *testing.B) {
		testutil.QuickBenchOnly(b, "skip load benchmark matrix in quick mode")

		meta := BenchLogMeta{
			Seed: BenchLogSeed{Count: 100_000, PayloadSize: 16},
			Options: Options{Storage: StorageOptions{
				MaxSegments:    0,
				MaxSegmentSize: 64 * 1024 * 1024,
			}},
		}

		SeedWritableBenchLog(b, meta)

		b.ResetTimer()
		for b.Loop() {
			l := GetReadOnlyBenchLog(b, meta)
			if err := l.Close(); err != nil {
				b.Fatalf("Log.Close() error = %v", err)
			}
		}
		testutil.ReportDomainMetrics(b, 100_000, int64(100_000*16))
	})

	for _, records := range []int{10_000, 100_000, 1_000_000} {
		b.Run("matrix/"+testutil.BenchmarkLabelInt("records", records), func(b *testing.B) {
			testutil.QuickBenchOnly(b, "skip load benchmark matrix in quick mode")

			meta := BenchLogMeta{
				Seed: BenchLogSeed{Count: records, PayloadSize: 16},
				Options: Options{Storage: StorageOptions{
					MaxSegments:    0,
					MaxSegmentSize: 256 * 1024 * 1024,
				}},
			}

			SeedWritableBenchLog(b, meta)

			b.ResetTimer()
			for b.Loop() {
				l := GetReadOnlyBenchLog(b, meta)
				if err := l.Close(); err != nil {
					b.Fatalf("Close() error = %v", err)
				}
			}
			testutil.ReportDomainMetrics(b, int64(records), int64(records*16))
		})
	}

	const totalRecords = 100_000
	for _, segments := range []int{1, 4, 16, 64, 256, 1024} {
		b.Run("segments/"+testutil.BenchmarkLabelInt("segments", segments), func(b *testing.B) {
			if segments >= 64 {
				testutil.QuickBenchOnly(b, "skip load benchmark for many segments in quick mode")
			}
			frameSize := int64(16 + 16)
			totalBytes := int64(totalRecords)*frameSize + 64
			maxSeg := max(totalBytes/int64(segments), frameSize+64)
			meta := BenchLogMeta{
				Seed: BenchLogSeed{Count: totalRecords, PayloadSize: 16},
				Options: Options{Storage: StorageOptions{
					MaxSegments:    0,
					MaxSegmentSize: maxSeg,
				}},
			}

			l := GetReadOnlyBenchLog(b, meta)

			if err := l.Close(); err != nil {
				b.Fatalf("Close() error = %v", err)
			}

			dir := l.Path()

			for b.Loop() {
				l, err := New(dir, meta.Options)
				if err != nil {
					b.Fatalf("NewLog() error = %v", err)
				}

				if err := l.Close(); err != nil {
					b.Fatalf("Close() error = %v", err)
				}
			}
		})
	}
}

func BenchmarkLog_TruncateBefore(b *testing.B) {
	testutil.QuickBenchOnly(b, "skip truncate benchmarks in quick mode")

	benchTruncatePrefix := func(b *testing.B, target uint64) {
		benchTruncate(b, target, 3, func(j *Log, seq uint64) error {
			return j.TruncateBefore(seq)
		})
	}

	b.Run("step=1", func(b *testing.B) { benchTruncatePrefix(b, 1) })
	b.Run("step=2", func(b *testing.B) { benchTruncatePrefix(b, 2) })
	b.Run("step=3", func(b *testing.B) { benchTruncatePrefix(b, 3) })
}

func BenchmarkLog_TruncateAfter(b *testing.B) {
	testutil.QuickBenchOnly(b, "skip truncate benchmarks in quick mode")

	// 4 records so every case removes at least one record
	benchTruncateSuffix := func(b *testing.B, target uint64) {
		benchTruncate(b, target, 4, func(j *Log, seq uint64) error {
			return j.TruncateAfter(seq)
		})
	}

	b.Run("near-tail", func(b *testing.B) { benchTruncateSuffix(b, 2) })
	b.Run("middle", func(b *testing.B) { benchTruncateSuffix(b, 1) })
	b.Run("near-head", func(b *testing.B) { benchTruncateSuffix(b, 0) })
}

func benchTruncate(b *testing.B, target uint64, records int, tfn func(*Log, uint64) error) {
	opts := DefaultOptions()
	opts.Storage.MaxSegmentSize = 64 * 1024 * 1024
	bench := BenchLogMeta{
		Seed:    BenchLogSeed{Count: records, PayloadSize: 16},
		Options: opts,
	}

	// fixture setup dwarfs the truncation, which would make b.Loop run far too many iterations
	p := testutil.NewPadder(b)

	b.ResetTimer()
	for b.Loop() {
		p.Pause()
		cp := GetWritableBenchLog(b, bench)
		p.Resume()

		if err := tfn(cp, target); err != nil {
			b.Fatalf("truncate error = %v", err)
		}

		p.Pause()
		if err := cp.Close(); err != nil {
			b.Fatalf("Log.Close() error = %v", err)
		}
		p.Resume()
	}

	p.Report()
}

func reportAppendMetrics(b *testing.B, recordsPerOp int64, bytesPerOp int64, _ float64) {
	b.Helper()
	testutil.ReportDomainMetrics(b, recordsPerOp, bytesPerOp)

	// if rawMBps <= 0 {
	// 	return
	// }
	//
	// elapsed := b.Elapsed().Seconds()
	// if elapsed <= 0 {
	// 	return
	// }
	//
	// b.ReportMetric(float64(b.N)/elapsed, "batches/s")

	// mbps := float64(bytesPerOp) * float64(b.N) / elapsed / float64(1<<20)
	// b.ReportMetric(mbps/rawMBps, "efficiency")
}

func benchMeta(maxSegments int, maxSegmentSize int64) BenchLogMeta {
	opts := DefaultOptions()
	opts.Storage.MaxSegments = maxSegments
	opts.Storage.MaxSegmentSize = maxSegmentSize
	return BenchLogMeta{Options: opts}
}

type appendRawBaselines struct {
	write      map[int]float64
	sync       map[int]float64
	batchWrite map[int]float64
	batchSync  map[int]float64
}

func benchmarkAppendRawBaselines(b *testing.B) appendRawBaselines {
	b.Helper()

	appendBaselineOnce.Do(func() {
		if cached, ok := loadAppendRawBaselinesCache(); ok {
			appendBaselineCache = cached
			return
		}

		appendBaselineCache = buildAppendRawBaselines(b)
		_ = writeAppendRawBaselinesCache(appendBaselineCache)
	})

	return appendBaselineCache
}

var (
	appendBaselineOnce  sync.Once
	appendBaselineCache appendRawBaselines
)

const appendBaselineCacheVersion = 2

func buildAppendRawBaselines(b *testing.B) appendRawBaselines {
	b.Helper()

	writeSizes := []int{
		16,
		64,
		256,
		4 << 10,
		64 << 10,
		1 << 20,
		8 * 256,
		64 * 256,
		256 * 256,
	}
	syncSizes := []int{
		16,
		256,
		4 << 10,
		1 << 20,
		8 * 256,
		64 * 256,
		256 * 256,
	}

	sort.Sort(sort.Reverse(sort.IntSlice(writeSizes)))
	sort.Sort(sort.Reverse(sort.IntSlice(syncSizes)))

	baseline := appendRawBaselines{
		write:      make(map[int]float64, len(writeSizes)),
		sync:       make(map[int]float64, len(syncSizes)),
		batchWrite: make(map[int]float64, 4),
		batchSync:  make(map[int]float64, 4),
	}

	for _, size := range writeSizes {
		payload := testutil.BenchmarkPayload(size)
		baseline.write[size] = measureRawWriteMBpsStable(b, payload, false, 256<<20)
	}

	for _, size := range syncSizes {
		payload := testutil.BenchmarkPayload(size)
		baseline.sync[size] = measureRawWriteMBpsStable(b, payload, true, 32<<20)
	}

	batchPayload := testutil.BenchmarkPayload(256)
	for _, records := range []int{1, 8, 64, 256} {
		parts, payloadBytes := rawBatchIOVParts(batchPayload, records)
		baseline.batchWrite[records] = measureRawWritevMBpsStable(b, parts, payloadBytes, false, 256<<20)
		baseline.batchSync[records] = measureRawWritevMBpsStable(b, parts, payloadBytes, true, 32<<20)
	}

	return baseline
}

type appendRawBaselinesCache struct {
	Write      map[int]float64 `json:"write"`
	Sync       map[int]float64 `json:"sync"`
	BatchWrite map[int]float64 `json:"batch_write"`
	BatchSync  map[int]float64 `json:"batch_sync"`
	Version    int             `json:"version"`
}

func loadAppendRawBaselinesCache() (appendRawBaselines, bool) {
	path, err := appendRawBaselinesCachePath()
	if err != nil {
		return appendRawBaselines{}, false
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return appendRawBaselines{}, false
	}

	var cached appendRawBaselinesCache
	if err = json.Unmarshal(raw, &cached); err != nil {
		return appendRawBaselines{}, false
	}

	if cached.Version != appendBaselineCacheVersion {
		return appendRawBaselines{}, false
	}

	result := appendRawBaselines{
		write:      cached.Write,
		sync:       cached.Sync,
		batchWrite: cached.BatchWrite,
		batchSync:  cached.BatchSync,
	}

	if !result.valid() {
		return appendRawBaselines{}, false
	}

	return result, true
}

func writeAppendRawBaselinesCache(baseline appendRawBaselines) error {
	if !baseline.valid() {
		return nil
	}

	path, err := appendRawBaselinesCachePath()
	if err != nil {
		return err
	}

	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	payload := appendRawBaselinesCache{
		Version:    appendBaselineCacheVersion,
		Write:      baseline.write,
		Sync:       baseline.sync,
		BatchWrite: baseline.batchWrite,
		BatchSync:  baseline.batchSync,
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, encoded, 0o644)
}

func appendRawBaselinesCachePath() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	if hostname == "" {
		return "", fmt.Errorf("empty hostname")
	}

	return filepath.Join("testdata", "bench", "append-baseline", hostname+".json"), nil
}

func (b appendRawBaselines) valid() bool {
	for _, size := range []int{16, 64, 256, 4 << 10, 64 << 10, 1 << 20, 8 * 256, 64 * 256, 256 * 256} {
		if b.write[size] <= 0 {
			return false
		}
	}

	for _, size := range []int{16, 256, 4 << 10, 1 << 20, 8 * 256, 64 * 256, 256 * 256} {
		if b.sync[size] <= 0 {
			return false
		}
	}

	for _, records := range []int{1, 8, 64, 256} {
		if b.batchWrite[records] <= 0 || b.batchSync[records] <= 0 {
			return false
		}
	}

	return true
}

func measureRawWriteMBpsStable(b *testing.B, payload []byte, syncEachWrite bool, targetBytes int) float64 {
	b.Helper()

	if targetBytes <= 0 {
		targetBytes = 64 * 1 << 20
	}

	// warm up cache and file-system path first
	_ = measureRawWriteMBps(b, payload, syncEachWrite, targetBytes)

	const samples = 7
	results := make([]float64, 0, samples)
	for range samples {
		results = append(results, measureRawWriteMBps(b, payload, syncEachWrite, targetBytes))
	}

	sort.Float64s(results)

	// average middle 3 samples to smooth single-point jitter
	return (results[2] + results[3] + results[4]) / 3
}

func measureRawWriteMBps(b *testing.B, payload []byte, syncEachWrite bool, targetBytes int) float64 {
	b.Helper()

	if len(payload) == 0 {
		return 0
	}

	iterations := targetBytes / len(payload)
	iterations = max(iterations, 1)
	if syncEachWrite {
		iterations = min(iterations, 512)
	} else {
		iterations = min(iterations, 1_048_576)
	}

	f := testutil.RawFileForBench(b)
	start := time.Now()

	for range iterations {
		if _, err := f.Write(payload); err != nil {
			b.Fatalf("raw baseline write error: %v", err)
		}
		if syncEachWrite {
			if err := f.Sync(); err != nil {
				b.Fatalf("raw baseline sync error: %v", err)
			}
		}
	}

	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0
	}

	return float64(iterations*len(payload)) / float64(1<<20) / elapsed
}

func rawBatchIOVParts(payload []byte, records int) (parts [][]byte, payloadBytes int) {
	headers := make([]byte, records*16)
	parts = make([][]byte, 0, records*2)
	for i := range records {
		hdr := headers[i*16 : (i+1)*16]
		parts = append(parts, hdr)
		parts = append(parts, payload)
	}

	return parts, records * len(payload)
}

func measureRawWritevMBpsStable(b *testing.B, parts [][]byte, payloadBytes int, syncEachWrite bool, targetBytes int) float64 {
	b.Helper()

	_ = measureRawWritevMBps(b, parts, payloadBytes, syncEachWrite, targetBytes)

	const samples = 7
	results := make([]float64, 0, samples)
	for range samples {
		results = append(results, measureRawWritevMBps(b, parts, payloadBytes, syncEachWrite, targetBytes))
	}

	sort.Float64s(results)

	return (results[2] + results[3] + results[4]) / 3
}

func measureRawWritevMBps(b *testing.B, parts [][]byte, payloadBytes int, syncEachWrite bool, targetBytes int) float64 {
	b.Helper()

	if payloadBytes <= 0 {
		return 0
	}

	iterations := targetBytes / payloadBytes
	iterations = max(iterations, 1)
	if syncEachWrite {
		iterations = min(iterations, 512)
	} else {
		iterations = min(iterations, 1_048_576)
	}

	f := testutil.RawFileForBench(b)
	start := time.Now()

	for range iterations {
		buffers := net.Buffers(parts)
		if _, err := buffers.WriteTo(f); err != nil {
			b.Fatalf("raw baseline writev error: %v", err)
		}
		if syncEachWrite {
			if err := f.Sync(); err != nil {
				b.Fatalf("raw baseline sync error: %v", err)
			}
		}
	}

	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0
	}

	return float64(iterations*payloadBytes) / float64(1<<20) / elapsed
}
