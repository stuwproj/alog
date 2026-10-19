# Scanning

`NewScanner(log)` iterates the retained records, oldest first, across segments.
It reads every payload completely, and verifies its checksum. `RangeFrom` is a
convenience on top of it, and `NewLimitedScanner` reads only the start of every
payload.

A scanner is not safe for concurrent use. Separate scanners are independent, and
can be created, seeked and scanned while the log is being appended to.

!!! warning "`Data()` is only valid until the next `Scan`"

    The buffer is reused. Copy it if you need to keep it. It holds a copy of the
    stored bytes, so it never points into a segment that has been recycled.

!!! warning "A scan that ends does not mean the log has ended"

    A scanner follows the log. When it has read everything there is, `Scan`
    returns false and `Err()` is nil, and a later call returns what was appended
    in the meantime, also when this is in a new segment. A loop that stops at
    the first false `Scan` reads what was there at that moment. To keep
    following the log, call `Scan` again later.

## Seeking and bounds

- `SeekSequence(seq)` positions the scanner at `seq`. A sequence before the
  first retained record positions it at the first record. A sequence at or after
  the next sequence of the log results in `ErrOutOfBounds`, and the scanner
  stays where it was.
- `StopAt(seq)` sets an exclusive end. The scan is over once it is reached,
  until `SeekSequence` starts a new one.
- `Close()` is cheap and can safely be called repeatedly. After `Close`, `Scan`
  returns false with `ErrClosed`, unless the scanner had read everything, in
  which case `Err()` stays nil. A closed scanner no longer follows the log.

Seeking uses the sparse index of checkpoints, and then reads forward from the
nearest one. The cost grows with `CheckpointStride`.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleNewScanner_seekSequenceAndStopAt:start -->
## NewScanner

### SeekSequenceAndStopAt

Go reference: [NewScanner](https://pkg.go.dev/stuw.net/alog#NewScanner).

The following example shows scanning a half-open sequence window.

```go
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
```
<!-- EXAMPLE:ExampleNewScanner_seekSequenceAndStopAt:end -->

<!-- prettier-ignore-end -->

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleScanner_SeekSequence:start -->
## Scanner

### SeekSequence

Go reference: [Scanner.SeekSequence](https://pkg.go.dev/stuw.net/alog#Scanner.SeekSequence).

The following example shows resuming a scan at a remembered sequence.

```go
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
```
<!-- EXAMPLE:ExampleScanner_SeekSequence:end -->

<!-- prettier-ignore-end -->

## Reading part of a payload

`NewLimitedScanner(log, n)` reads at most the first `n` bytes of every payload,
so `Data()` is shorter than the record when this is longer than `n`. With an `n`
of 0 only the frame headers are read, which gives the sequences without touching
any payload. It is meant for records that start with a header of their own,
which the reader validates independently.

!!! warning "A limited scanner does not verify payloads"

    The checksum covers the whole payload, so it cannot be checked when only the
    start of it is read. The frame headers are still trusted: they were
    validated when the log was loaded, and a scanner never reads beyond what a
    completed append has written. The bytes of the payload are not. A full
    scanner reports damage as an "invalid checksum" error, a limited scanner
    returns the damaged bytes.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleNewLimitedScanner:start -->
## NewLimitedScanner

Go reference: [NewLimitedScanner](https://pkg.go.dev/stuw.net/alog#NewLimitedScanner).

The following example shows reading only the leading header of every payload.

```go
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
```
<!-- EXAMPLE:ExampleNewLimitedScanner:end -->

<!-- prettier-ignore-end -->

## Reading a range

`RangeFrom(log, start, end, fn)` calls `fn` for every record in `[start, end)`.
An `end` of 0, or beyond the log, means up to the end of the log as it is when
the call starts: records that are appended while it runs are not visited. A
`start` before the first retained record starts at the first record. It stops at
the first error returned by `fn`, and returns this error. It has the same
semantics as a scanner, so the payload is only valid during the callback.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleRangeFrom:start -->
## RangeFrom

Go reference: [RangeFrom](https://pkg.go.dev/stuw.net/alog#RangeFrom).

The following example shows stateless bounded range reads.

```go
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
```
<!-- EXAMPLE:ExampleRangeFrom:end -->

<!-- prettier-ignore-end -->

## Concurrent reads and retention

A scanner only reads up to the end of the last completed append, so it never
sees a batch that is being written, or one that failed and was rolled back.
Every record is checksum-verified when it is read by a full scanner, and a
damaged payload fails the scan with an "invalid checksum" error.

!!! warning "Slow readers lose their place"

    Appends do not wait for readers. When `MaxSegments` recycles a segment (or
    the log is closed) a scanner that is reading it stops with
    `ErrSegmentReaped`.

Segments are read through a read-only `mmap` of the file, and payloads are
copied out of it. Handing out the mapped bytes directly would be faster, but
they would dangle as soon as a segment is recycled, and the cost of copying is
small. A lease on each segment makes reads and closing or recycling the segment
exclude each other, so a mapping is never released under a reader.

## Errors

| Error              | Returned when                                                                                 |
| ------------------ | --------------------------------------------------------------------------------------------- |
| `ErrOutOfBounds`   | A seek at or after the next sequence of the log.                                              |
| `ErrSegmentReaped` | The scanner's segment was recycled, or the log was closed, or records it read were truncated. |
| `ErrClosed`        | The scanner was closed.                                                                       |
