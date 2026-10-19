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
	"errors"
	"io"
	"testing"

	"stuw.net/alog"
	"github.com/stretchr/testify/require"
)

func TestRangeFrom(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	l, err := alog.New(dir, alog.DefaultOptions())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	_, err = l.Append([]byte("a"), []byte("b"), []byte("c"))
	require.NoError(t, err)

	count := 0
	err = alog.RangeFrom(l, 0, 0, func(uint64, []byte) error {
		count++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, count)

	count = 0
	err = alog.RangeFrom(l, 2, 2, func(uint64, []byte) error {
		count++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 0, count)

	var seqs []uint64
	collect := func(seq uint64, _ []byte) error {
		seqs = append(seqs, seq)
		return nil
	}

	require.NoError(t, alog.RangeFrom(l, 1, 3, collect))
	require.Equal(t, []uint64{1, 2}, seqs, "the start is included and the end is not")

	seqs = nil
	require.NoError(t, alog.RangeFrom(l, 0, 1, collect))
	require.Equal(t, []uint64{0}, seqs)

	seqs = nil
	require.NoError(t, alog.RangeFrom(l, 2, 100, collect))
	require.Equal(t, []uint64{2}, seqs)

	// records that are appended while it runs are not visited
	seqs = nil
	require.NoError(t, alog.RangeFrom(l, 0, 0, func(seq uint64, _ []byte) error {
		seqs = append(seqs, seq)
		_, appendErr := l.Append([]byte("more"))
		return appendErr
	}))
	require.Equal(t, []uint64{0, 1, 2}, seqs)

	stopErr := errors.New("stop")
	count = 0
	err = alog.RangeFrom(l, 0, 0, func(seq uint64, _ []byte) error {
		count++
		if seq == 1 {
			return stopErr
		}
		return nil
	})
	require.ErrorIs(t, err, stopErr)
	require.Equal(t, 2, count)
}

func BenchmarkRangeFrom(b *testing.B) {
	l := alog.GetReadOnlyBenchLog(
		b,
		alog.BenchLogMeta{
			Seed: alog.BenchLogSeed{
				Count:       20_000,
				PayloadSize: 16,
			},
			Options: alog.DefaultOptions(),
		},
	)

	for _, tc := range []struct {
		name  string
		start uint64
		end   uint64
	}{{"head", 0, 1}, {"window", 1000, 1001}} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				err := alog.RangeFrom(l, tc.start, tc.end, func(uint64, []byte) error {
					return nil
				})
				if err != nil && !errors.Is(err, io.EOF) {
					b.Fatalf("range: %v", err)
				}
			}
		})
	}
}
