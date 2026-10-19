# Writing

Sequence numbers are `uint64` and are assigned in order, starting at
`StartSequence` (0 by default). A call to `AppendOne`, `Append`,
`AppendMultipart` or `AppendMultipartBatch` writes 1 batch: a single write, all
or nothing. After a crash the batch is either completely present or not present
at all. A call without payloads writes nothing.

A `Log` is not safe for concurrent use. Calls to `Append*`, `Sync`, `Truncate*`,
`Close` and the accessors (`FirstSequence`, `LastSequence`, `NextSequence`,
`DiskSize`) have to be serialized. With more than 1 producer, use a
[`Writer`](#concurrent-writes).

!!! warning "`Append` returns the last sequence of the batch"

    Not the first. The records of a batch have consecutive sequences, so the
    first one is the returned sequence minus the number of records, plus 1.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleLog_Append_andSync:start -->
## Log

### Append

Go reference: [Log.Append](https://pkg.go.dev/stuw.net/alog#Log.Append).

The following example shows appending a batch and syncing it to durable storage.

```go
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
```
<!-- EXAMPLE:ExampleLog_Append_andSync:end -->

<!-- prettier-ignore-end -->

## Durability

!!! warning "Appends are not durable by default"

    Set `SyncOnAppend`, or call `Log.Sync()`. An unsynced batch may be lost in a
    crash, but it is never visible partially.

With `SyncOnAppend`, every append is synced before it returns. Without it,
`Log.Sync()` syncs everything written so far. A new segment is created with its
header and end marker written, after which the file and its directory are
synced.

!!! note "macOS"

    Syncing uses `F_FULLFSYNC`, which flushes the drive cache. A plain `fsync`
    does not do this on macOS. As a result durable appends are a lot slower than
    on Linux.

## Frames

Records are stored as frames: a 16-byte header followed by the payload,
big-endian:

```text
offset  size  field
0       4     payload length, or checkpoint CRC
4       2     index stride, or checkpoint stride
6       2     reserved
8       2     frame generation
10      2     flags
12      4     CRC32C of header bytes 0-11 and the payload
```

The flags say what kind of frame this is. Any other combination is invalid, and
a header without flags was never written.

| Kind                    | Flags                | Bytes 4-6         |
| ----------------------- | -------------------- | ----------------- |
| data                    | `Data`               | index stride 1    |
| data, last of its batch | `Data` + `BatchEnd`  | index stride 1    |
| checkpoint              | `Checkpoint`         | checkpoint stride |
| end of log              | `Checkpoint` + `EOF` | checkpoint stride |

The frame generation is bumped when a segment file is recycled. Frames with a
different generation are treated as the end of the data, which is how stale
frames behind a recycled segment's new content are ignored.

### Batches and checkpoints

One append is one `pwritev` at the current end of the log:

```text
frame frame frame* [checkpoint] EOF        * = BatchEnd
```

The end marker is an EOF checkpoint. The next append overwrites it, so the log
always ends in exactly 1 EOF checkpoint.

The end of a batch is marked with a flag bit on its last frame, not with a frame
of its own. An extra 16 bytes per batch is negligible for large batches, but not
for small frames in small batches.

A checkpoint holds the CRC32C of every frame header since the previous
checkpoint. It is written at the end of the batch that completes
`CheckpointStride` records, which means **every checkpoint is a batch
boundary**. Recovery needs a place where it can safely cut, and the middle of a
batch is not such a place. The EOF checkpoint covers the headers after the last
checkpoint in the same way, without resetting the running CRC.

Checkpoints make headers verifiable without reading any payload, and they are
the entries of the sparse index that seeking uses.

!!! warning "A large batch leaves a long gap between checkpoints"

    Seeking and crash recovery both work from the last checkpoint, so both are
    slower in this case.

## Payload size

A record larger than `MaxPayloadSize` (see
[configuration](./loading.md#configuration)) is rejected with
`ErrPayloadTooLarge` before anything is written. For a multipart payload all the
parts count. When a batch contains 1 record that is too large, the whole batch
is rejected.

The limit applies to what is appended with the current options. It is not stored
in the segments, so changing it does not affect records that are already there,
and scanners are not bound by it.

## Rotation and retention

A batch that does not fit in the tail goes to a new segment, and is never split
over segments. This means a batch has to fit in an empty segment, otherwise the
append fails with `ErrBatchTooLarge`. The limit is the file size, including the
segment header and the end marker.

`MaxDiskSize` and `MaxSegments` decide what happens as the log grows:

| `MaxDiskSize` | `MaxSegmentSize` | `MaxSegments` | Behavior                                                                  |
| ------------- | ---------------- | ------------- | ------------------------------------------------------------------------- |
| `> 0`         | `> 0`            | `0`           | Rotates by size. Appends fail with `ErrDiskFull` at the budget.           |
| `> 0`         | `> 0`            | `> 1`         | The same, and the oldest segment is recycled once the count is reached.   |
| `<= 0`        | `> 0`            | `0`           | Rotates by size, grows without bound.                                     |
| `<= 0`        | `> 0`            | `> 1`         | Rotates by size and keeps at most that many segments, like a ring buffer. |
| any           | `> 0`            | `1`           | A single segment, `ErrSegmentFull` when it is full.                       |

Recycling reuses the oldest segment's file, with a new frame generation. A
recycled segment is gone for readers, whose scanners stop with
[`ErrSegmentReaped`](./scanning.md#concurrent-reads-and-retention).

!!! warning "A full log rejects appends instead of discarding old data"

    The defaults cap the log at 10 GiB, after which appends fail with
    `ErrDiskFull`. `MaxDiskSize` never deletes anything. Set `MaxSegments`, or
    use `TruncateBefore`, if old data should go away.

## Truncation

`TruncateBefore(seq)` removes all records below `seq`. `TruncateAfter(seq)`
removes all records above it, so `seq` itself is retained.

- `TruncateBefore` deletes whole segments below the sequence. In the segment
  that contains the sequence, the read offset and start sequence are moved. This
  is persisted in the metadata slot, together with the checkpoint state at the
  new offset. The file keeps its size.
- `TruncateAfter` removes later records by writing an EOF checkpoint at the cut.
  If the cut lands inside a batch, a regular checkpoint is written first. This
  makes the cut a batch boundary, so recovery never rolls back past it.

`TruncateBefore` with a sequence before the first record does nothing, and with
`NextSequence()` it empties the log. A sequence beyond that results in
`ErrOutOfBounds`. `TruncateAfter` cannot empty the log, and a sequence outside
the retained records results in `ErrOutOfBounds`.

!!! warning "`TruncateAfter` frees up sequence numbers"

    Subsequent appends reuse them. Anything that stored a sequence beyond the
    cut will find different data there. A scanner that already read beyond the
    cut stops with `ErrSegmentReaped`, while a scanner that is positioned at or
    before it carries on, and reads the new records.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleLog_TruncateBefore:start -->
### TruncateBefore

Go reference: [Log.TruncateBefore](https://pkg.go.dev/stuw.net/alog#Log.TruncateBefore).

The following example shows prefix and suffix truncation.

```go
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
```
<!-- EXAMPLE:ExampleLog_TruncateBefore:end -->

<!-- prettier-ignore-end -->

## Failed appends

If the write or the sync fails, the tracker and the index are rolled back, and
the old end marker is written back and synced, replacing whatever part of the
batch reached the file. If _this_ fails as well, nothing on disk can be trusted
anymore: the log is marked as failed, and refuses appends with `ErrLogFailed`
until it has been closed and opened again. Opening runs recovery.

A truncation that fails after the file may have changed does the same, since the
file and what the log has in memory no longer agree. Appending then would put
records behind an end marker, which loading drops. Scanners are not affected,
they stop at the cut.

## Concurrent writes

`NewWriter(log, opts...)` and `Start(ctx)` provide a single write path for
concurrent producers. Requests are collected into a batch, which is written in
one call. `Append(ctx, data)` and `AppendMultipart(ctx, parts)` return the
sequence of their record once the batch has been written. Sequences within a
batch are consecutive, in order of arrival.

A batch is flushed when any of these limits is reached:

| Option                | Default | Flush when                                        |
| --------------------- | ------- | ------------------------------------------------- |
| `WithWriterMaxLength` | 500     | this many requests are queued                     |
| `WithWriterMaxSize`   | 4 MiB   | this many payload bytes are queued                |
| `WithWriterMaxDelay`  | 5 ms    | this much time has passed since the first request |

- The writer does not sync by itself. With `SyncOnAppend` every batch is synced
  once, which is what makes concurrent durable appends cheap.
- `WithWriterMaxLength` is capped at 500, which is what fits in a single write.
- A batch that is too large for the log is written in parts, so a request that
  cannot be written does not fail the others. Any other error fails every
  request in the batch with the same error.
- When the context ends, the queued batch is flushed and later calls return
  `ErrStopped`.
- Do not append to the same log directly while a writer is running.
- A request that is larger than `MaxPayloadSize` is rejected right away, and
  does not affect the other requests in its batch.

!!! warning "A single request waits for the max delay"

    A request waits up to 5 ms by default for other requests to join its batch,
    so a lone caller pays this latency in full. Lower the max delay if latency
    matters.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleWriter:start -->
## Writer

Use `alog.NewWriter(...).Start(ctx)` for concurrent append workloads with grouped buffering.
The writer batches appends according to buffer thresholds to reduce write
amplification while preserving ordered sequence assignment.

Go reference: [Writer](https://pkg.go.dev/stuw.net/alog#Writer).

The following example shows concurrent-safe buffered appends.

```go
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
```
<!-- EXAMPLE:ExampleWriter:end -->

<!-- prettier-ignore-end -->

## Errors

| Error                | Returned when                                                                                                                                                                                        |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ErrPayloadTooLarge` | A record is larger than `MaxPayloadSize`.                                                                                                                                                            |
| `ErrBatchTooLarge`   | The batch cannot be written as one: it has more than 65535 records, or needs more than about 1000 buffers for a single write, or cannot fit in an empty segment (then joined with `ErrSegmentFull`). |
| `ErrSegmentFull`     | The only allowed segment (`MaxSegments == 1`) is full.                                                                                                                                               |
| `ErrDiskFull`        | The append would exceed `MaxDiskSize`.                                                                                                                                                               |
| `ErrLogFailed`       | A failed append could not be rolled back, or a truncation failed. Reopen the log.                                                                                                                    |
| `ErrOutOfBounds`     | `TruncateBefore` beyond `NextSequence()`, or `TruncateAfter` outside the retained records.                                                                                                           |
| `ErrStopped`         | The writer was stopped.                                                                                                                                                                              |
