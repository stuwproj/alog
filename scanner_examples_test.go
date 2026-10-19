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
	"fmt"
	"os"

	"stuw.net/alog"
)

// ExampleNewScanner_seekSequenceAndStopAt shows scanning a half-open sequence window.
func ExampleNewScanner_seekSequenceAndStopAt() {
	dir, err := os.MkdirTemp("", "alog-example-scan-")
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

	for _, v := range []string{"a", "b", "c", "d", "e"} {
		if _, err = l.Append([]byte(v)); err != nil {
			fmt.Printf("append: %v\n", err)
			return
		}
	}

	s, err := alog.NewScanner(l)
	if err != nil {
		fmt.Printf("new scanner: %v\n", err)
		return
	}
	if err = s.SeekSequence(2); err != nil {
		fmt.Printf("seek sequence: %v\n", err)
		return
	}
	s.StopAt(3)

	for s.Scan() {
		fmt.Printf("%d:%s\n", s.LastSequence(), s.Data())
	}

	// Output:
	// 2:c
}

// ExampleScanner_SeekSequence shows resuming a scan at a remembered sequence.
func ExampleScanner_SeekSequence() {
	dir, err := os.MkdirTemp("", "alog-example-offset-")
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

	if _, err = l.Append([]byte("a"), []byte("b")); err != nil {
		fmt.Printf("append: %v\n", err)
		return
	}

	probe, err := alog.NewScanner(l)
	if err != nil {
		fmt.Printf("new scanner: %v\n", err)
	}

	if !probe.Scan() {
		fmt.Printf("scan: %v\n", probe.Err())
		return
	}

	offset := probe.LastSequence()

	s, err := alog.NewScanner(l)
	if err != nil {
		fmt.Printf("new scanner: %v\n", err)
		return
	}

	if err = s.SeekSequence(offset); err != nil {
		fmt.Printf("seek offset: %v\n", err)
		return
	}

	if !s.Scan() {
		fmt.Printf("scan: %v\n", s.Err())
		return
	}

	fmt.Printf("%d:%s\n", s.LastSequence(), s.Data())

	// Output:
	// 0:a
}

// ExampleNewLimitedScanner shows reading only the leading header of every payload.
func ExampleNewLimitedScanner() {
	dir, err := os.MkdirTemp("", "alog-example-limited-")
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

	// every record starts with a header of its own, followed by a body
	if _, err = l.Append([]byte("h1:a long body"), []byte("h2:another long body")); err != nil {
		fmt.Printf("append: %v\n", err)
		return
	}

	s, err := alog.NewLimitedScanner(l, 3)
	if err != nil {
		fmt.Printf("new scanner: %v\n", err)
		return
	}

	for s.Scan() {
		fmt.Printf("%d:%s\n", s.LastSequence(), s.Data())
	}

	// Output:
	// 0:h1:
	// 1:h2:
}
