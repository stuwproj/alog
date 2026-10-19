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
	"context"
	"fmt"
	"os"
	"time"

	"stuw.net/alog"
)

// ExampleWriter shows concurrent-safe buffered appends.
//
// Use `alog.NewWriter(...).Start(ctx)` for concurrent append workloads with grouped buffering.
// The writer batches appends according to buffer thresholds to reduce write
// amplification while preserving ordered sequence assignment.
func ExampleWriter() {
	dir, err := os.MkdirTemp("", "alog-example-writer-")
	if err != nil {
		fmt.Printf("mkdir temp: %v\n", err)
		return
	}
	defer os.RemoveAll(dir) //nolint:errcheck

	l, err := alog.New(dir, alog.DefaultOptions())
	if err != nil {
		fmt.Printf("load log: %v\n", err)
		return
	}
	defer l.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := alog.NewWriter(
		l,
		alog.WithWriterMaxLength(16),
		alog.WithWriterMaxSize(4<<10),
		alog.WithWriterMaxDelay(5*time.Millisecond),
	)
	w.Start(ctx)

	seq0, err := w.Append(ctx, []byte("one"))
	if err != nil {
		fmt.Printf("append one: %v\n", err)
		return
	}

	seq1, err := w.Append(ctx, []byte("two"))
	if err != nil {
		fmt.Printf("append two: %v\n", err)
		return
	}

	fmt.Println(seq0, seq1)

	// Output:
	// 0 1
}
