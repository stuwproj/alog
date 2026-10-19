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

package segment

import (
	"errors"
	"path/filepath"
	"testing"

	"stuw.net/alog/internal/frame"
	"stuw.net/alog/internal/scanner"
	"github.com/stretchr/testify/require"
)

type failOnValidate struct{}

func (failOnValidate) Validate(*frame.Header, []byte) error {
	return errors.New("validator fail")
}

func TestSegment_NewScannerWithValidator(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(0))
	s, err := create(path, 0, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)

	t.Run("uses default validator when nil", func(t *testing.T) {
		sc, err := s.NewScannerWithValidator(0, nil, true)
		require.NoError(t, err)
		require.True(t, sc.Scan())
		require.NoError(t, sc.Err())
		require.NoError(t, sc.Close())
	})

	t.Run("uses provided validator", func(t *testing.T) {
		sc, err := s.NewScannerWithValidator(0, failOnValidate{}, true)
		require.NoError(t, err)
		require.False(t, sc.Scan())
		require.ErrorContains(t, sc.Err(), "validator fail")
		require.NoError(t, sc.Close())
	})
}

func TestSegment_NewScanner(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), segmentFileName(5))
	s, err := create(path, 5, nil, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.AppendOne([]byte("a"))
	require.NoError(t, err)

	sc, err := s.NewScanner(0, true)
	require.NoError(t, err)
	require.IsType(t, &scanner.ChecksumValidator{}, sc.Validator)
	require.NoError(t, sc.Close())
}
