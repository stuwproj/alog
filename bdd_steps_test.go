// MIT License
//
// # Copyright (C) 2025 John Kleijn
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
package alog_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"stuw.net/alog"
)

var errCallback = errors.New("callback failed")

var namedErrors = map[string]error{
	"ErrSegmentFull":     alog.ErrSegmentFull,
	"ErrDiskFull":        alog.ErrDiskFull,
	"ErrBatchTooLarge":   alog.ErrBatchTooLarge,
	"ErrPayloadTooLarge": alog.ErrPayloadTooLarge,
	"ErrLocked":          alog.ErrLocked,
	"ErrOutOfBounds":     alog.ErrOutOfBounds,
	"ErrClosed":          alog.ErrClosed,
	"ErrStopped":         alog.ErrStopped,
}

type state struct {
	first    uint64
	hasFirst bool
	next     uint64
}

type record struct {
	seq     uint64
	payload string
}

type world struct {
	dir  string
	opts alog.Options
	log  *alog.Log

	second *alog.Log

	lastSeq uint64
	err     error
	before  state

	diskBefore int64

	scanner *alog.Scanner

	visited  []string
	rangeErr error

	writer *alog.Writer
	cancel context.CancelFunc

	producerSeqs [][]uint64
	reader       []uint64
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}

	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		*w = world{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, s *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})

	w.registerGiven(sc)
	w.registerAppend(sc)
	w.registerLifecycle(sc)
	w.registerTruncate(sc)
	w.registerAssertions(sc)
	w.registerScanning(sc)
	w.registerConcurrency(sc)
}

func (w *world) cleanup() {
	if w.cancel != nil {
		w.cancel()
	}
	if w.scanner != nil {
		_ = w.scanner.Close()
	}
	if w.second != nil {
		_ = w.second.Close()
	}
	if w.log != nil {
		_ = w.log.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
	w.scanner, w.second, w.log, w.dir = nil, nil, nil, ""
}

func baseOptions() alog.Options {
	return alog.NewOptions(alog.WithStorage(alog.StorageOptions{
		MaxSegmentSize:   1 << 20,
		CheckpointStride: 16,
	}))
}

func (w *world) open(opts alog.Options) error {
	l, err := alog.New(w.dir, opts)
	if err != nil {
		return err
	}
	w.log = l
	w.opts = opts
	return nil
}

func (w *world) openEmpty(mod func(*alog.StorageOptions)) error {
	dir, err := os.MkdirTemp("", "alog-bdd-")
	if err != nil {
		return err
	}
	w.dir = dir

	opts := baseOptions()
	if mod != nil {
		mod(&opts.Storage)
	}
	return w.open(opts)
}

func (w *world) reopen(mod func(*alog.StorageOptions)) error {
	if err := w.log.Close(); err != nil {
		return err
	}
	w.log = nil

	opts := w.opts
	if mod != nil {
		mod(&opts.Storage)
	}
	return w.open(opts)
}

func (w *world) snapshot() state {
	first, has := w.log.FirstSequence()
	return state{first: first, hasFirst: has, next: w.log.NextSequence()}
}

func quoted(list string) []string {
	matches := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(list, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func numbers(list string) ([]uint64, error) {
	var out []uint64
	for _, f := range strings.Split(list, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func payloadOf(size int, tag int) []byte {
	b := bytes.Repeat([]byte{'x'}, size)
	copy(b, fmt.Sprintf("%d:", tag))
	return b
}

func (w *world) scanAll() ([]record, error) {
	s, err := alog.NewScanner(w.log)
	if err != nil {
		return nil, err
	}
	defer s.Close() //nolint:errcheck

	var out []record
	for s.Scan() {
		out = append(out, record{seq: s.LastSequence(), payload: string(s.Data())})
	}
	return out, s.Err()
}

func payloads(records []record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.payload
	}
	return out
}

func (w *world) record(seq uint64, err error) error {
	w.lastSeq, w.err = seq, err
	return nil
}

func (w *world) appendBytes(payloads ...[]byte) error {
	w.before = w.snapshot()
	return w.record(w.log.Append(payloads...))
}

func (w *world) mustAppend(payloads ...[]byte) error {
	if err := w.appendBytes(payloads...); err != nil {
		return err
	}
	return w.err
}

func (w *world) registerGiven(sc *godog.ScenarioContext) {
	sc.Step(`^an empty log$`, func() error { return w.openEmpty(nil) })

	sc.Step(`^an empty log starting at sequence (\d+)$`, func(n uint64) error {
		return w.openEmpty(func(o *alog.StorageOptions) { o.StartSequence = n })
	})

	sc.Step(`^an empty log without preallocation$`, func() error {
		return w.openEmpty(func(o *alog.StorageOptions) { o.Preallocate = -1 })
	})

	sc.Step(`^an empty log with a maximum payload size of (\d+) bytes$`, func(n int64) error {
		return w.openEmpty(func(o *alog.StorageOptions) { o.MaxPayloadSize = n })
	})

	sc.Step(`^an empty log with segments of (\d+) bytes$`, func(n int64) error {
		return w.openEmpty(func(o *alog.StorageOptions) { o.MaxSegmentSize = n })
	})

	sc.Step(`^an empty log with segments of (\d+) bytes and at most (\d+) segments?$`, func(n int64, m int) error {
		return w.openEmpty(func(o *alog.StorageOptions) { o.MaxSegmentSize, o.MaxSegments = n, m })
	})

	sc.Step(`^the log contains ((?:"[^"]*"(?:, )?)+)$`, func(list string) error {
		var batch [][]byte
		for _, p := range quoted(list) {
			batch = append(batch, []byte(p))
		}
		return w.mustAppend(batch...)
	})

	sc.Step(`^the log contains (\d+) records of (\d+) bytes$`, func(count, size int) error {
		return w.appendMany(count, size)
	})
}

func (w *world) appendMany(count, size int) error {
	for i := range count {
		if err := w.mustAppend(payloadOf(size, i)); err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
	}
	return nil
}

func (w *world) registerAppend(sc *godog.ScenarioContext) {
	sc.Step(`^I append "([^"]*)"$`, func(p string) error {
		return w.appendBytes([]byte(p))
	})

	sc.Step(`^I append the batch ((?:"[^"]*"(?:, )?)+)$`, func(list string) error {
		var batch [][]byte
		for _, p := range quoted(list) {
			batch = append(batch, []byte(p))
		}
		return w.appendBytes(batch...)
	})

	sc.Step(`^I append the multipart record ((?:"[^"]*"(?:, )?)+)$`, func(list string) error {
		var parts alog.MultipartPayload
		for _, p := range quoted(list) {
			parts = append(parts, []byte(p))
		}
		w.before = w.snapshot()
		return w.record(w.log.AppendMultipart(parts))
	})

	sc.Step(`^I append a record of (\d+) bytes$`, func(n int) error {
		return w.appendBytes(payloadOf(n, 0))
	})

	sc.Step(`^I append a batch of (\d+) records of (\d+) bytes$`, func(count, size int) error {
		batch := make([][]byte, count)
		for i := range batch {
			batch[i] = payloadOf(size, i)
		}
		return w.appendBytes(batch...)
	})

	sc.Step(`^I append (\d+) records of (\d+) bytes$`, func(count, size int) error {
		return w.appendMany(count, size)
	})

	sc.Step(`^I append records of (\d+) bytes until the append fails$`, func(size int) error {
		for i := range 10000 {
			if err := w.appendBytes(payloadOf(size, i)); err != nil {
				return err
			}
			if w.err != nil {
				return nil
			}
		}
		return errors.New("append never failed")
	})
}

func (w *world) registerLifecycle(sc *godog.ScenarioContext) {
	sc.Step(`^I close and reopen the log$`, func() error { return w.reopen(nil) })

	sc.Step(`^I close and reopen the log with start sequence (\d+)$`, func(n uint64) error {
		return w.reopen(func(o *alog.StorageOptions) { o.StartSequence = n })
	})

	sc.Step(`^I close and reopen the log read only$`, func() error {
		return w.reopen(func(o *alog.StorageOptions) { o.ReadOnly = true })
	})

	sc.Step(`^I close the log$`, func() error {
		err := w.log.Close()
		w.log = nil
		return err
	})

	sc.Step(`^I sync the log$`, func() error { return w.log.Sync() })

	sc.Step(`^I open a second log on the same directory$`, func() error {
		w.second, w.err = alog.New(w.dir, w.opts)
		return nil
	})

	sc.Step(`^opening fails with (\w+)$`, func(name string) error {
		return w.expectError(name)
	})

	sc.Step(`^opening succeeds$`, func() error {
		if w.err != nil {
			return fmt.Errorf("open failed: %w", w.err)
		}
		return nil
	})
}

func (w *world) registerTruncate(sc *godog.ScenarioContext) {
	sc.Step(`^I truncate before sequence (\d+)$`, func(n uint64) error {
		w.diskBefore = w.log.DiskSize()
		return w.log.TruncateBefore(n)
	})

	sc.Step(`^I truncate after sequence (\d+)$`, func(n uint64) error {
		w.diskBefore = w.log.DiskSize()
		return w.log.TruncateAfter(n)
	})
}

func (w *world) expectError(name string) error {
	want, ok := namedErrors[name]
	if !ok {
		return fmt.Errorf("unknown error %q", name)
	}
	if !errors.Is(w.err, want) {
		return fmt.Errorf("expected %s, got %v", name, w.err)
	}
	return nil
}

func (w *world) registerAssertions(sc *godog.ScenarioContext) {
	sc.Step(`^the log has no records$`, func() error {
		if _, has := w.log.FirstSequence(); has {
			return errors.New("log has records")
		}
		return nil
	})

	sc.Step(`^the first sequence is (\d+)$`, func(n uint64) error {
		got, has := w.log.FirstSequence()
		if !has || got != n {
			return fmt.Errorf("first sequence is %d (has records: %t), want %d", got, has, n)
		}
		return nil
	})

	sc.Step(`^the first sequence is at most (\d+)$`, func(n uint64) error {
		got, has := w.log.FirstSequence()
		if !has || got > n {
			return fmt.Errorf("first sequence is %d (has records: %t), want at most %d", got, has, n)
		}
		return nil
	})

	sc.Step(`^the last sequence is (\d+)$`, func(n uint64) error {
		got, has := w.log.LastSequence()
		if !has || got != n {
			return fmt.Errorf("last sequence is %d (has records: %t), want %d", got, has, n)
		}
		return nil
	})

	sc.Step(`^the next sequence is (\d+)$`, func(n uint64) error {
		if got := w.log.NextSequence(); got != n {
			return fmt.Errorf("next sequence is %d, want %d", got, n)
		}
		return nil
	})

	sc.Step(`^the append returned sequence (\d+)$`, func(n uint64) error {
		if w.err != nil {
			return w.err
		}
		if w.lastSeq != n {
			return fmt.Errorf("append returned %d, want %d", w.lastSeq, n)
		}
		return nil
	})

	sc.Step(`^the append fails with (\w+)$`, func(name string) error { return w.expectError(name) })

	sc.Step(`^appending "([^"]*)" fails$`, func(p string) error {
		if _, err := w.log.AppendOne([]byte(p)); err == nil {
			return errors.New("append succeeded")
		}
		return nil
	})

	sc.Step(`^the log has not changed$`, func() error {
		if got := w.snapshot(); got != w.before {
			return fmt.Errorf("log changed from %+v to %+v", w.before, got)
		}
		return nil
	})

	sc.Step(`^the log is still readable$`, func() error {
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return errors.New("log has no records")
		}
		return nil
	})

	sc.Step(`^the log holds (\d+) records$`, func(n int) error {
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		if len(records) != n {
			return fmt.Errorf("log holds %d records, want %d", len(records), n)
		}
		return nil
	})

	sc.Step(`^the sequences are consecutive$`, func() error {
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		for i := 1; i < len(records); i++ {
			if records[i].seq != records[i-1].seq+1 {
				return fmt.Errorf("sequence %d follows %d", records[i].seq, records[i-1].seq)
			}
		}
		return nil
	})

	sc.Step(`^the log uses more than one segment file$`, func() error {
		entries, err := os.ReadDir(w.dir)
		if err != nil {
			return err
		}
		var files int
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".bin") {
				files++
			}
		}
		if files < 2 {
			return fmt.Errorf("found %d segment files in %v", files, entries)
		}
		return nil
	})

	sc.Step(`^the log uses less disk space than before$`, func() error {
		if got := w.log.DiskSize(); got >= w.diskBefore {
			return fmt.Errorf("disk size %d, was %d", got, w.diskBefore)
		}
		return nil
	})
}

func (w *world) currentScanner() (*alog.Scanner, error) {
	if w.scanner == nil {
		s, err := alog.NewScanner(w.log)
		if err != nil {
			return nil, err
		}
		w.scanner = s
	}
	return w.scanner, nil
}

func drain(s *alog.Scanner) ([]record, error) {
	var out []record
	for s.Scan() {
		out = append(out, record{seq: s.LastSequence(), payload: string(s.Data())})
	}
	return out, s.Err()
}

func expectPayloads(got []record, want []string) error {
	if !reflect.DeepEqual(payloads(got), append([]string{}, want...)) && !(len(got) == 0 && len(want) == 0) {
		return fmt.Errorf("got %q, want %q", payloads(got), want)
	}
	return nil
}

func (w *world) registerScanning(sc *godog.ScenarioContext) {
	list := `((?:"[^"]*"(?:, )?)+)`

	sc.Step(`^scanning the log yields `+list+`$`, func(l string) error {
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		return expectPayloads(records, quoted(l))
	})

	sc.Step(`^the scanned sequences are ([\d, ]+)$`, func(l string) error {
		want, err := numbers(l)
		if err != nil {
			return err
		}
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		got := make([]uint64, len(records))
		for i, r := range records {
			got[i] = r.seq
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("got %v, want %v", got, want)
		}
		return nil
	})

	sc.Step(`^I seek to sequence (\d+)$`, func(n uint64) error {
		s, err := w.currentScanner()
		if err != nil {
			return err
		}
		w.err = s.SeekSequence(n)
		return nil
	})

	sc.Step(`^I seek to sequence (\d+) and stop at sequence (\d+)$`, func(from, to uint64) error {
		s, err := w.currentScanner()
		if err != nil {
			return err
		}
		if err = s.SeekSequence(from); err != nil {
			return err
		}
		s.StopAt(to)
		return nil
	})

	sc.Step(`^seeking fails with (\w+)$`, func(name string) error { return w.expectError(name) })

	sc.Step(`^scanning yields `+list+`$`, func(l string) error {
		if w.err != nil {
			return w.err
		}
		s, err := w.currentScanner()
		if err != nil {
			return err
		}
		records, err := drain(s)
		if err != nil {
			return err
		}
		return expectPayloads(records, quoted(l))
	})

	sc.Step(`^a scanner that has read everything$`, func() error {
		s, err := w.currentScanner()
		if err != nil {
			return err
		}
		_, err = drain(s)
		return err
	})

	sc.Step(`^a scanner at the start$`, func() error {
		_, err := w.currentScanner()
		return err
	})

	sc.Step(`^I close the scanner$`, func() error { return w.scanner.Close() })

	sc.Step(`^scanning fails with (\w+)$`, func(name string) error {
		if w.scanner.Scan() {
			return errors.New("scan succeeded")
		}
		w.err = w.scanner.Err()
		return w.expectError(name)
	})

	sc.Step(`^limited scanning from sequence (\d+) with a limit of (\d+) bytes yields `+list+`$`,
		func(from uint64, limit uint32, l string) error {
			s, err := alog.NewLimitedScanner(w.log, limit)
			if err != nil {
				return err
			}
			defer s.Close() //nolint:errcheck

			if err = s.SeekSequence(from); err != nil {
				return err
			}
			records, err := drain(s)
			if err != nil {
				return err
			}
			return expectPayloads(records, quoted(l))
		})

	w.registerRange(sc, list)
}

func (w *world) registerRange(sc *godog.ScenarioContext, list string) {
	visit := func(p []byte) { w.visited = append(w.visited, string(p)) }

	sc.Step(`^I range over sequences (\d+) to (\d+)$`, func(from, to uint64) error {
		w.rangeErr = alog.RangeFrom(w.log, from, to, func(_ uint64, p []byte) error {
			visit(p)
			return nil
		})
		return nil
	})

	sc.Step(`^I range from sequence (\d+) to the end$`, func(from uint64) error {
		w.rangeErr = alog.RangeFrom(w.log, from, 0, func(_ uint64, p []byte) error {
			visit(p)
			return nil
		})
		return nil
	})

	sc.Step(`^I range over the log and fail on "([^"]*)"$`, func(on string) error {
		w.rangeErr = alog.RangeFrom(w.log, 0, 0, func(_ uint64, p []byte) error {
			visit(p)
			if string(p) == on {
				return errCallback
			}
			return nil
		})
		return nil
	})

	sc.Step(`^the range visited `+list+`$`, func(l string) error {
		want := quoted(l)
		if !reflect.DeepEqual(w.visited, want) {
			return fmt.Errorf("visited %q, want %q", w.visited, want)
		}
		return nil
	})

	sc.Step(`^the range returned the callback error$`, func() error {
		if !errors.Is(w.rangeErr, errCallback) {
			return fmt.Errorf("range returned %v", w.rangeErr)
		}
		return nil
	})
}

func (w *world) registerConcurrency(sc *godog.ScenarioContext) {
	sc.Step(`^a started writer$`, func() error {
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel = cancel
		w.writer = alog.NewWriter(w.log)
		w.writer.Start(ctx)
		return nil
	})

	sc.Step(`^(\d+) producers each append (\d+) records through the writer$`, func(producers, count int) error {
		return w.produce(producers, count, false)
	})

	sc.Step(`^(\d+) producers each append (\d+) records through the writer while a reader scans$`,
		func(producers, count int) error { return w.produce(producers, count, true) })

	sc.Step(`^every producer received distinct sequences$`, func() error {
		seen := map[uint64]bool{}
		for _, seqs := range w.producerSeqs {
			for _, s := range seqs {
				if seen[s] {
					return fmt.Errorf("sequence %d was assigned twice", s)
				}
				seen[s] = true
			}
		}
		return nil
	})

	sc.Step(`^the records of each producer are in the order they were appended$`, func() error {
		records, err := w.scanAll()
		if err != nil {
			return err
		}
		next := map[string]int{}
		for _, r := range records {
			producer, n, _ := strings.Cut(r.payload, "/")
			want := strconv.Itoa(next[producer])
			if n != want {
				return fmt.Errorf("producer %s: got record %s, want %s", producer, n, want)
			}
			next[producer]++
		}
		return nil
	})

	sc.Step(`^the reader saw consecutive sequences$`, func() error {
		for i := 1; i < len(w.reader); i++ {
			if w.reader[i] != w.reader[i-1]+1 {
				return fmt.Errorf("reader saw %d after %d", w.reader[i], w.reader[i-1])
			}
		}
		return nil
	})

	sc.Step(`^the writer is stopped$`, func() error {
		w.cancel()
		return nil
	})

	sc.Step(`^appending through the writer fails$`, func() error {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, err := w.writer.Append(ctx, []byte("x"))
			cancel()
			if err != nil {
				return nil
			}
		}
		return errors.New("append through a stopped writer kept succeeding")
	})
}

func (w *world) produce(producers, count int, withReader bool) error {
	w.producerSeqs = make([][]uint64, producers)

	var readerDone chan struct{}
	stopReader := make(chan struct{})
	if withReader {
		readerDone = make(chan struct{})
		go func() {
			defer close(readerDone)
			w.readWhileWriting(stopReader)
		}()
	}

	var wg sync.WaitGroup
	errs := make(chan error, producers)

	for p := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range count {
				seq, err := w.writer.Append(context.Background(), []byte(fmt.Sprintf("p%d/%d", p, i)))
				if err != nil {
					errs <- err
					return
				}
				w.producerSeqs[p] = append(w.producerSeqs[p], seq)
			}
		}()
	}

	wg.Wait()
	close(stopReader)
	if readerDone != nil {
		<-readerDone
	}
	close(errs)

	return <-errs
}

func (w *world) readWhileWriting(stop <-chan struct{}) {
	s, err := alog.NewScanner(w.log)
	if err != nil {
		return
	}
	defer s.Close() //nolint:errcheck

	stopping := false
	for {
		for s.Scan() {
			w.reader = append(w.reader, s.LastSequence())
		}
		if stopping {
			return
		}
		select {
		case <-stop:
			stopping = true
		case <-time.After(time.Millisecond):
		}
	}
}
