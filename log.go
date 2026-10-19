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
	"errors"
	"fmt"
	"os"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/segment"
	"stuw.net/alog/internal/storage"
)

// ErrSegmentFull is returned when the segment is full
// and a new one could not be created.
var ErrSegmentFull = segment.ErrSegmentFull

// ErrDiskFull is returned when retained byte budget cannot admit an append.
var ErrDiskFull = segment.ErrDiskFull

// ErrBatchTooLarge is returned when a batch cannot be written as one: it cannot fit in an
// empty segment (together with ErrSegmentFull), it has more than 65535 records, or it
// needs more buffers than a single write takes (about 1000).
var ErrBatchTooLarge = segment.ErrBatchTooLarge

// ErrPayloadTooLarge is returned when a record is larger than MaxPayloadSize, or
// than a frame can hold.
var ErrPayloadTooLarge = segment.ErrPayloadTooLarge

// ErrLocked is returned when the directory of a log is in use by another log, in this
// process or in another one. A log that is read only does not take the lock.
var ErrLocked = errors.New("log directory is in use by another log")

// ErrLogFailed is returned by appends and truncations after a failed write could not be
// rolled back, or after a truncation failed.
// The state of the log on disk is then unknown. Close the log and open it again,
// which recovers it.
var ErrLogFailed = segment.ErrFailed

// Log is responsible for loading existing segments from disk and managing them.
type Log struct {
	path    string
	chain   *segment.Chain
	lock    *os.File
	scratch []frame.MultiPartPayload

	// maxPayloadSize is the largest record that is accepted, 0 or smaller is no limit
	maxPayloadSize int64
}

// MultipartPayload represents one logical record split into multiple byte parts.
type MultipartPayload = frame.MultiPartPayload

// New creates and loads a Log from storageLocation with the given options.
func New(path string, opts Options) (*Log, error) {
	opts = normalizeOptions(opts)

	if opts.Storage.MaxSegmentSize > 0 && opts.Storage.Preallocate > opts.Storage.MaxSegmentSize {
		return nil, fmt.Errorf(
			"preallocate size %d cannot be larger than max segment size %d",
			opts.Storage.Preallocate,
			opts.Storage.MaxSegmentSize,
		)
	}

	if capacity := segment.EmptySegmentCapacity(
		opts.Storage.MaxSegmentSize,
		opts.Storage.CheckpointStride,
	); opts.Storage.MaxSegmentSize > 0 &&
		opts.Storage.MaxPayloadSize > 0 && opts.Storage.MaxPayloadSize > capacity {
		return nil, fmt.Errorf(
			"max payload size %d cannot be larger than what fits in an empty segment: %d",
			opts.Storage.MaxPayloadSize,
			capacity,
		)
	}

	var lock *os.File

	if !opts.Storage.ReadOnly {
		var err error

		if lock, err = lockDirectory(path); err != nil {
			return nil, err
		}
	}

	chain, err := segment.NewChain(
		opts.Storage.MaxSegmentSize,
		opts.Storage.MaxDiskSize,
		opts.Storage.MaxSegments,
		segment.NewLoader(
			segment.NewFactory(
				opts.Storage.Preallocate,
				opts.Storage.SyncOnAppend,
				opts.Storage.CheckpointStride,
			),
			path,
			opts.Storage.StartSequence,
			storage.NewOSFileSystem(opts.Storage.ReadOnly),
		),
	)
	if err != nil {
		err = fmt.Errorf("failed to create chain: %w", err)

		// the directory is not in use by this log
		if lock != nil {
			err = errors.Join(err, lock.Close())
		}

		return nil, err
	}

	return &Log{
		path:           path,
		chain:          chain,
		lock:           lock,
		scratch:        opts.Scratch,
		maxPayloadSize: opts.Storage.MaxPayloadSize,
	}, nil
}

func normalizeOptions(opts Options) Options {
	defaults := DefaultOptions()

	if opts.Storage == (StorageOptions{}) {
		opts.Storage = defaults.Storage
	}

	if opts.Storage.CheckpointStride == 0 {
		opts.Storage.CheckpointStride = defaults.Storage.CheckpointStride
	}

	if opts.Storage.Preallocate == 0 {
		opts.Storage.Preallocate = defaultMaxSegmentSize

		if opts.Storage.MaxSegmentSize > 0 {
			opts.Storage.Preallocate = opts.Storage.MaxSegmentSize
		}
	}

	if opts.Storage.MaxPayloadSize == 0 {
		opts.Storage.MaxPayloadSize = defaultMaxPayloadSize(opts.Storage.MaxSegmentSize, opts.Storage.CheckpointStride)
	}

	return opts
}

func defaultMaxPayloadSize(maxSegmentSize int64, checkpointStride uint16) int64 {
	if maxSegmentSize <= 0 {
		return defaultUnboundedPayloadSize
	}

	capacity := segment.EmptySegmentCapacity(maxSegmentSize, checkpointStride)
	if capacity <= 0 {
		// nothing fits, which is reported when appending
		return -1
	}

	return min(max(maxSegmentSize/defaultPayloadSegmentFactor, defaultMinMaxPayloadSize), capacity)
}

// checkPayloadSize rejects a record that is larger than the maximum payload size.
func (log *Log) checkPayloadSize(size int64) error {
	if log.maxPayloadSize > 0 && size > log.maxPayloadSize {
		return fmt.Errorf("%w: %d bytes, the maximum is %d", ErrPayloadTooLarge, size, log.maxPayloadSize)
	}

	return nil
}

// Path returns the storage directory path backing this log.
func (log *Log) Path() string {
	return log.path
}

// FirstSequence returns the first retained sequence number and whether any records exist.
func (log *Log) FirstSequence() (uint64, bool) {
	head := log.chain.Head()
	if head == nil {
		return 0, false
	}

	tail := log.chain.Tail()
	if head.StartSequence() >= tail.NextSequence() {
		return 0, false
	}

	return head.StartSequence(), true
}

// LastSequence returns the last retained sequence number and whether any records exist.
func (log *Log) LastSequence() (uint64, bool) {
	head := log.chain.Head()
	if head == nil {
		return 0, false
	}

	tail := log.chain.Tail()
	if head.StartSequence() >= tail.NextSequence() {
		return 0, false
	}

	return tail.NextSequence() - 1, true
}

// AppendOne writes one payload and returns the assigned sequence.
// It is not safe for concurrent use; use NewWriter(...).Start(ctx) for concurrent producers.
func (log *Log) AppendOne(payload []byte) (lastSeq uint64, err error) {
	if err = log.checkPayloadSize(int64(len(payload))); err != nil {
		return 0, err
	}

	seq, err := log.chain.AppendOne(payload)
	if err != nil {
		return 0, mapAppendError(err, "failed to append payload")
	}

	return seq, nil
}

func (log *Log) appendBatch(payloads [][]byte) (lastSeq uint64, err error) {
	if len(payloads) == 0 {
		return 0, nil
	}

	if len(payloads) == 1 {
		return log.AppendOne(payloads[0])
	}

	if len(payloads) > len(log.scratch) {
		log.scratch = make([]frame.MultiPartPayload, len(payloads))
		for i := range log.scratch {
			log.scratch[i] = make(frame.MultiPartPayload, 1)
		}
	}

	for i, payload := range payloads {
		log.scratch[i][0] = payload
	}

	lastSeq, err = log.AppendMultipartBatch(log.scratch[0:len(payloads)])

	// do not keep the payloads of the caller alive until the next append
	for i := range payloads {
		log.scratch[i][0] = nil
	}

	return lastSeq, err
}

// Append is a convenience method for AppendMultipartBatch()
func (log *Log) Append(payloads ...[]byte) (lastSeq uint64, err error) {
	return log.appendBatch(payloads)
}

// AppendMultipart is a convenience method for AppendMultipartBatch()
func (log *Log) AppendMultipart(payloads ...MultipartPayload) (lastSeq uint64, err error) {
	return log.AppendMultipartBatch(payloads)
}

// AppendMultipartBatch writes multipart payload records as one batch and returns the last assigned sequence.
// It is not safe for concurrent use; use NewWriter(...).Start(ctx) for concurrent producers.
func (log *Log) AppendMultipartBatch(payloads []MultipartPayload) (lastSeq uint64, err error) {
	if len(payloads) == 0 {
		return 0, nil
	}

	for _, payload := range payloads {
		if err = log.checkPayloadSize(payload.Size()); err != nil {
			return 0, err
		}
	}

	seq, err := log.chain.AppendBatch(payloads)
	if err != nil {
		return 0, mapAppendError(err, "failed to append multipart payloads")
	}

	return seq, nil
}

func mapAppendError(err error, context string) error {
	return fmt.Errorf("%s: %w", context, err)
}

// Sync flushes file contents and metadata of all open segments to stable storage.
func (log *Log) Sync() error {
	return log.chain.Sync()
}

// TruncateBefore removes all records with sequence numbers less than the specified seq from the log.
func (log *Log) TruncateBefore(seq uint64) error {
	return log.chain.TruncateBefore(seq)
}

// TruncateAfter removes all records with sequence numbers greater than the specified seq from the log.
func (log *Log) TruncateAfter(seq uint64) error {
	return log.chain.TruncateAfter(seq)
}

// Close closes all segments in the log and returns any errors that occurred during closing.
func (log *Log) Close() error {
	err := log.chain.Close()

	// releases the lock, and does nothing the second time
	if log.lock != nil {
		err = errors.Join(err, log.lock.Close())
		log.lock = nil
	}

	return err
}

// readable returns the first sequence, and the sequence after the last one, that
// scanners can read as of the last append. Unlike the accessors of the log it can
// be called while the log is appended to.
func (log *Log) readable() (first, next uint64) {
	snapshot := log.chain.Snapshot()

	return snapshot.Head().PublishedStart(), snapshot.Tail().PublishedNext()
}

// NextSequence returns the exclusive next sequence at tail.
func (log *Log) NextSequence() uint64 {
	tail := log.chain.Tail()
	if tail == nil {
		return 0
	}

	return tail.NextSequence()
}

// DiskSize returns the retained physical size in bytes of segment files.
func (log *Log) DiskSize() int64 {
	return log.chain.DiskSize()
}
