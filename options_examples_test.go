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

package alog_test

import (
	"stuw.net/alog"
)

// ExampleOptions_default shows using `DefaultOptions` as a baseline.
//
// This is a good starting point for most deployments: segment rotation is
// bounded, retained bytes are capped, and checkpoint indexing is sparse.
func ExampleOptions_default() {
	opts := alog.DefaultOptions()
	_ = opts // nodoc
}

// ExampleNewOptions shows constructing defaults with modifiers.
func ExampleNewOptions() {
	opts := alog.NewOptions(
		func(o *alog.Options) {
			o.Storage.MaxSegmentSize = 8 * 1024 * 1024
		},
		func(o *alog.Options) {
			o.Storage.MaxSegments = 2
		},
	)
	_ = opts // nodoc
}

// ExampleOptions_singleSegmentSync shows single-segment mode with synchronous durability.
//
// This configuration keeps exactly one segment and rejects appends when full,
// while `SyncOnAppend` fsyncs each write before it returns.
func ExampleOptions_singleSegmentSync() {
	opts := alog.DefaultOptions()
	opts.Storage.MaxSegments = 1
	opts.Storage.MaxSegmentSize = 64 * 1024 * 1024
	opts.Storage.SyncOnAppend = true
	_ = opts // nodoc
}

// ExampleOptions_boundedRotationUnboundedDisk shows bounded segment sizes with unbounded retained bytes.
//
// This rotates by segment size but leaves total retained bytes unbounded,
// which can be useful when external retention handles pruning.
func ExampleOptions_boundedRotationUnboundedDisk() {
	opts := alog.DefaultOptions()
	opts.Storage.MaxDiskSize = 0
	opts.Storage.MaxSegmentSize = 128 * 1024 * 1024
	opts.Storage.MaxSegments = 0
	_ = opts // nodoc
}
