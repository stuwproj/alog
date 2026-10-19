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

// MIT License

package alog_test

import (
	"fmt"
	"os"

	"stuw.net/alog"
)

// ExampleLog_Append_andSync shows appending a batch and syncing it to durable storage.
func ExampleLog_Append_andSync() {
	dir, err := os.MkdirTemp("", "alog-example-append-")
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

	lastSeq, err := l.Append([]byte("alpha"), []byte("beta"), []byte("gamma"))
	if err != nil {
		fmt.Printf("append: %v\n", err)
		return
	}

	if err = l.Sync(); err != nil {
		fmt.Printf("sync: %v\n", err)
		return
	}

	fmt.Println(lastSeq)

	// Output:
	// 2
}

// ExampleLog_FirstSequence shows sequence inspection and retained-size reporting before and after appends.
func ExampleLog_FirstSequence() {
	dir, err := os.MkdirTemp("", "alog-example-sequences-")
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

	if first, ok := l.FirstSequence(); ok {
		fmt.Printf("first=%d\n", first)
	} else {
		fmt.Println("first=none")
	}
	fmt.Printf("next=%d\n", l.NextSequence())
	fmt.Printf("disk=%d\n", l.DiskSize())

	if _, err = l.Append([]byte("a"), []byte("b"), []byte("c")); err != nil {
		fmt.Printf("append: %v\n", err)
		return
	}

	first, _ := l.FirstSequence()
	last, _ := l.LastSequence()
	fmt.Printf("first=%d\n", first)
	fmt.Printf("last=%d\n", last)
	fmt.Printf("next=%d\n", l.NextSequence())
	fmt.Printf("disk=%d\n", l.DiskSize())

	// Output:
	// first=none
	// next=0
	// disk=134217728
	// first=0
	// last=2
	// next=3
	// disk=134217728
}

// ExampleLog_TruncateBefore shows prefix and suffix truncation.
func ExampleLog_TruncateBefore() {
	dir, err := os.MkdirTemp("", "alog-example-truncate-")
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

	if _, err = l.Append([]byte("a"), []byte("b"), []byte("c"), []byte("d")); err != nil {
		fmt.Printf("append: %v\n", err)
		return
	}

	if err = l.TruncateBefore(1); err != nil {
		fmt.Printf("truncate before: %v\n", err)
		return
	}
	if err = l.TruncateAfter(2); err != nil {
		fmt.Printf("truncate after: %v\n", err)
		return
	}

	err = alog.RangeFrom(l, 0, 0, func(seq uint64, payload []byte) error {
		fmt.Printf("%d:%s\n", seq, payload)
		return nil
	})
	if err != nil {
		fmt.Printf("scan: %v\n", err)
	}

	// Output:
	// 1:b
	// 2:c
}
