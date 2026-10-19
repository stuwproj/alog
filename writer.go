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
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrStopped is returned when the writer has been stopped and cannot accept new requests.
	ErrStopped = errors.New("writer stopped")
)

const (
	// maxWriterBatchLength is the number of requests that fit in a single write: a batch
	// of plain requests takes 2 buffers per request, and the write takes at most 1024.
	maxWriterBatchLength   = 500
	defaultWriterMaxLength = maxWriterBatchLength
	defaultWriterMaxSize   = 4 * 1024 * 1024
)

const defaultWriterMaxDelay = 5 * time.Millisecond

type response struct {
	err error
	seq uint64
}

type request struct {
	done      chan struct{}
	resp      response
	data      []byte
	multipart MultipartPayload
}

// WriterOption applies a configuration mutation to Writer setup.
type WriterOption func(*writerConfig)

type writerConfig struct {
	maxLength int
	maxSize   int64
	maxDelay  time.Duration
}

// WithWriterMaxLength configures the buffered record count flush threshold.
// It is capped at 500, which is what fits in a single write.
func WithWriterMaxLength(maxLength int) WriterOption {
	return func(cfg *writerConfig) {
		if maxLength > 0 {
			cfg.maxLength = min(maxLength, maxWriterBatchLength)
		}
	}
}

// WithWriterMaxSize configures the buffered byte-size flush threshold.
func WithWriterMaxSize(maxSize int64) WriterOption {
	return func(cfg *writerConfig) {
		if maxSize > 0 {
			cfg.maxSize = maxSize
		}
	}
}

// WithWriterMaxDelay configures the maximum time a buffered batch may wait.
func WithWriterMaxDelay(maxDelay time.Duration) WriterOption {
	return func(cfg *writerConfig) {
		if maxDelay > 0 {
			cfg.maxDelay = maxDelay
		}
	}
}

// Writer buffers concurrent append requests and forwards batches to the
// configured append target.
type Writer struct {
	log       *Log
	inPool    *sync.Pool
	requestCh chan *request
	stopCh    chan struct{}
	// scratch is the batch that is handed to the log. It refers to the slices of
	// the requests, and to singles, but never owns what it points to.
	scratch []MultipartPayload
	// singles are 1-element payloads for requests that have a single part. These are
	// the only slices that are modified, callers keep ownership of their own.
	singles []MultipartPayload
	// one is the batch of a single multipart request
	one       [1]MultipartPayload
	maxLength int
	maxSize   int64
	maxDelay  time.Duration
	startOnce sync.Once
}

// NewWriter creates a new writer instance for buffered concurrent appends.
func NewWriter(log *Log, options ...WriterOption) *Writer {
	cfg := writerConfig{
		maxLength: defaultWriterMaxLength,
		maxSize:   defaultWriterMaxSize,
		maxDelay:  defaultWriterMaxDelay,
	}

	for _, opt := range options {
		if opt == nil {
			continue
		}

		opt(&cfg)
	}

	return &Writer{
		log:       log,
		requestCh: make(chan *request),
		stopCh:    make(chan struct{}),
		maxLength: cfg.maxLength,
		maxSize:   cfg.maxSize,
		maxDelay:  cfg.maxDelay,
		inPool: &sync.Pool{
			New: func() any {
				return &request{
					done: make(chan struct{}, 1),
				}
			},
		},
	}
}

// Append queues a payload and returns its assigned sequence when the batch flushes.
func (l *Writer) Append(ctx context.Context, data []byte) (uint64, error) {
	// rejected here, so it can not fail the requests it would share a batch with
	if err := l.log.checkPayloadSize(int64(len(data))); err != nil {
		return 0, err
	}

	req := l.inPool.Get().(*request) // nolint: errcheck
	req.data = data

	return l.enqueue(ctx, req)
}

// AppendMultipart queues a multipart payload and returns its assigned sequence when the batch flushes.
func (l *Writer) AppendMultipart(ctx context.Context, payload MultipartPayload) (uint64, error) {
	if err := l.log.checkPayloadSize(payload.Size()); err != nil {
		return 0, err
	}

	req := l.inPool.Get().(*request) // nolint: errcheck
	req.multipart = payload

	return l.enqueue(ctx, req)
}

func (l *Writer) enqueue(ctx context.Context, req *request) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	select {
	case <-l.stopCh:
		return 0, ErrStopped
	case <-ctx.Done():
		return 0, ctx.Err()
	case l.requestCh <- req:
	}

	<-req.done
	resp := req.resp
	seq, err := resp.seq, resp.err

	req.data = nil
	req.multipart = nil
	resp.err = nil
	resp.seq = 0

	l.inPool.Put(req)

	return seq, err

}

// Start a background goroutine that listens for data on the buffer channel
func (l *Writer) Start(ctx context.Context) {
	l.startOnce.Do(func() {
		go l.flushLoop(ctx)
	})
}

func (l *Writer) flushLoop(ctx context.Context) {
	defer close(l.stopCh)

	batch := make([]*request, 0, l.maxLength)
	var size int64

	timer := newLazyTimer(l.maxDelay)
	flush := func() {
		if len(batch) > 0 {
			l.writeBatch(batch)

			clear(batch)
			batch = batch[:0]
			size = 0
		}

		timer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-timer.C:
			flush()
		case req := <-l.requestCh:
			if len(batch) == 0 {
				timer.Start()
			}

			batch = append(batch, req)
			size += int64(len(req.data))
			for _, part := range req.multipart {
				size += int64(len(part))
			}

			if size < l.maxSize && len(batch) < l.maxLength {
				continue
			}

			flush()
		}
	}
}

// writeBatch synchronously writes a batch of requests.
func (l *Writer) writeBatch(batch []*request) {
	if len(batch) == 1 {
		req := batch[0]
		seq, err := l.writeOne(req)
		req.resp = response{seq: seq, err: err}
		req.done <- struct{}{}
		return
	}

	if len(batch) > len(l.scratch) {
		l.scratch = make([]MultipartPayload, len(batch))
		l.singles = make([]MultipartPayload, len(batch))

		for i := range l.singles {
			l.singles[i] = make(MultipartPayload, 1)
		}
	}

	for i, req := range batch {
		if req.multipart != nil {
			l.scratch[i] = req.multipart
			continue
		}

		l.singles[i][0] = req.data
		l.scratch[i] = l.singles[i]
	}

	lastSeq, err := l.log.AppendMultipartBatch(l.scratch[:len(batch)])

	// do not keep the payloads of the requests alive until the next batch
	clear(l.scratch[:len(batch)])

	for i := range batch {
		l.singles[i][0] = nil
	}

	// Nothing was written when a batch is too large, it is only too large as a
	// whole. Write its parts, so the requests that are fine do not fail with it.
	if errors.Is(err, ErrBatchTooLarge) {
		mid := len(batch) / 2

		l.writeBatch(batch[:mid])
		l.writeBatch(batch[mid:])

		return
	}

	if err != nil {
		resp := response{err: err}

		for _, req := range batch {
			req.resp = resp
			req.done <- struct{}{}
		}

		return
	}

	seq := lastSeq - uint64(len(batch)) + 1
	for _, req := range batch {
		req.resp = response{seq: seq}
		req.done <- struct{}{}
		seq++
	}
}

func (l *Writer) writeOne(req *request) (uint64, error) {
	if req.multipart != nil {
		l.one[0] = req.multipart
		seq, err := l.log.AppendMultipartBatch(l.one[:])
		l.one[0] = nil

		return seq, err
	}

	return l.log.AppendOne(req.data)
}
