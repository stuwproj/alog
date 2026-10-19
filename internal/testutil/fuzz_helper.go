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

package testutil

import (
	"encoding/binary"
	"hash/fnv"
)

// BoundedFuzzBytes returns a slice of the input bytes that is at
// most maxLen in length.
func BoundedFuzzBytes(raw []byte, maxLen int, salt uint32) []byte {
	if len(raw) <= maxLen {
		return raw
	}

	h := fnv.New32a()
	_, _ = h.Write(raw)

	saltBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(saltBytes, salt)

	_, _ = h.Write(saltBytes)

	hash := int(h.Sum32())
	y := len(raw) - maxLen + 1
	start := hash % y

	return raw[start : start+maxLen]
}
