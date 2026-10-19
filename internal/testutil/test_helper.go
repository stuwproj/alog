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

package testutil

import (
	"context"
	"math/rand"
	"testing"
)

// MinUint64 returns the minimum of two uint64 values.
func MinUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// MaxUint64 returns the maximum of two uint64 values.
func MaxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// ContextWithCancel returns a context with cancel function for testing/benching purposes.
func ContextWithCancel(t testing.TB) (context.Context, func()) {
	t.Helper()
	return context.WithCancel(t.Context())
}

// RandomSeqs generates a slice of n random uint64 values, each less than the specified upper limit.
func RandomSeqs(n int, upper uint64) []uint64 {
	r := rand.New(rand.NewSource(42))
	seqs := make([]uint64, n)
	for i := range n {
		seqs[i] = uint64(r.Int63n(int64(upper)))
	}
	return seqs
}
