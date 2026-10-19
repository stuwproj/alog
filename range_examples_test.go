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

package alog_test

import (
	"fmt"
	"os"

	"stuw.net/alog"
)

// ExampleRangeFrom shows stateless bounded range reads.
func ExampleRangeFrom() {
	dir, err := os.MkdirTemp("", "alog-example-range-")
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

	err = alog.RangeFrom(l, 1, 0, func(seq uint64, payload []byte) error {
		fmt.Printf("%d:%s\n", seq, payload)
		if seq == 1 {
			return fmt.Errorf("stop")
		}
		return nil
	})
	if err != nil {
		return
	}

	// Output:
	// 1:b
}
