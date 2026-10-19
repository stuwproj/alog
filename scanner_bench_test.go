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

package alog

import (
	"fmt"
	"testing"
)

func BenchmarkScanner_SeekSequence(b *testing.B) {
	l := GetReadOnlyBenchLog(
		b,
		BenchLogMeta{
			Seed:    BenchLogSeed{Count: 20_000, PayloadSize: 16},
			Options: DefaultOptions(),
		},
	)

	positions := []uint64{0, 100, 10_000}
	for _, pos := range positions {
		b.Run(fmt.Sprintf("seq=%d", pos), func(b *testing.B) {
			s, err := NewScanner(l)
			if err != nil {
				b.Fatalf("failed to create scanner: %v", err)
			}

			for b.Loop() {
				if err := s.SeekSequence(pos); err != nil {
					b.Fatalf("seek error: %v", err)
				}
			}
		})
	}
}
