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

const (
	defaultMaxDiskSize      int64  = 10 * 1024 * 1024 * 1024
	defaultMaxSegments      int    = 0
	defaultMaxSegmentSize   int64  = 128 * 1024 * 1024
	defaultCheckpointStride uint16 = 4096

	// The default maximum payload size is a fraction of the segment size, so a segment holds many
	// maximum size records, with a floor for small segments. Without a segment size limit
	// it is a fixed size.
	defaultPayloadSegmentFactor int64 = 64
	defaultMinMaxPayloadSize    int64 = 64 * 1024
	defaultUnboundedPayloadSize int64 = 1024 * 1024
)

// Option applies a configuration mutation to Options.
type Option func(*Options)

// Options defines the configuration options for the log.
type Options struct {
	Scratch []MultipartPayload
	Storage StorageOptions
}

// StorageOptions defines configuration for segment loading, creation, and retention.
type StorageOptions struct {
	// CheckpointStride controls the number of records between each header CRC checkpoint.
	// Checkpoints are control frames that track the CRC of frame headers since the last checkpoint,
	// which dramatically reduces load times at a tiny append-time cost. A value of 0 selects the
	// default of 4096, checkpoints can not be disabled. A larger value means fewer checkpoints, and
	// slower seeks and crash recovery. Note that this is a target value: checkpoints are only written
	// at the end of an append batch, so a batch that completes the stride (or exceeds it) is followed
	// by one checkpoint. Checkpoints are also used to create sparse offset indexes, and mark the
	// points to which a segment is rolled back after an interrupted append.
	CheckpointStride uint16

	// SyncOnAppend controls whether each append call fsyncs segment data before returning.
	// When false, callers can issue explicit durability barriers via Log.Sync().
	SyncOnAppend bool

	// ReadOnly controls whether segment files are opened in read-only mode.
	// When true, mutating operations are rejected by the storage layer.
	ReadOnly bool

	// StartSequence is the sequence number assigned to the first record in a
	// newly created log (that is, when no segment files exist yet).
	// Existing logs ignore this value and keep their on-disk sequence space.
	StartSequence uint64

	// MaxDiskSize is the maximum retained physical size of all segment files.
	// A value of 0 or smaller means disk retention is unbounded.
	MaxDiskSize int64

	// MaxSegments is the maximum number of segments that can be stored in the log.
	// A value of 0 or smaller means no explicit segment-count limit.
	// A value of 1 means that the log will only have one segment, and appending will be rejected
	// when the segment is full, assuming MaxSegmentSize is set to a positive value.
	MaxSegments int

	// MaxSegmentSize is the maximum size of a segment file in bytes, including the segment header and
	// the end marker. A batch is never split over segments, so it has to fit in an empty segment.
	// When a segment reaches this size,
	// a new segment will be created, unless max number of segments is 1,
	// in which case appending will be rejected. A value of 0 or smaller means there is no limit
	// on the segment size. MaxSegments must then be set explicitly to 1.
	MaxSegmentSize int64

	// Preallocate is the size of the preallocated space for each segment file.
	// When set to 0 it defaults to the MaxSegmentSize value, or to 128 MiB when
	// MaxSegmentSize is not limited.
	// To disable preallocation, set this value to a negative number.
	// This number can not be larger than the MaxSegmentSize when MaxSegmentSize is a positive value.
	Preallocate int64

	// MaxPayloadSize is the maximum size in bytes of a single record, all parts included.
	// Appending a larger record is rejected with ErrPayloadTooLarge, before anything is written.
	// A value of 0 selects a default of MaxSegmentSize/64, but at least 64 KiB, and never more than
	// fits in an empty segment. It is 1 MiB when MaxSegmentSize is not limited. A negative value
	// removes the limit, apart from the size of an empty segment.
	// It is not persisted: it only applies to what is appended with these options.
	// This number can not be larger than what fits in an empty segment of MaxSegmentSize.
	MaxPayloadSize int64
}

// NewOptions builds options from defaults and applies modifiers.
func NewOptions(mods ...Option) Options {
	o := Options{
		Storage: StorageOptions{
			MaxDiskSize:      defaultMaxDiskSize,
			MaxSegments:      defaultMaxSegments,
			MaxSegmentSize:   defaultMaxSegmentSize,
			CheckpointStride: defaultCheckpointStride,
		},
	}

	for _, mod := range mods {
		if mod == nil {
			continue
		}
		mod(&o)
	}

	return o
}

// WithStorage applies a custom storage configuration.
func WithStorage(s StorageOptions) Option {
	return func(o *Options) {
		o.Storage = s
	}
}

// DefaultOptions returns a baseline configuration that is suitable for
// most local and server workloads and can be tweaked per deployment.
func DefaultOptions() Options {
	return NewOptions()
}
