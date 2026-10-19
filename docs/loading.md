# Loading

`alog.New(dir, opts)` opens the log in `dir`. If there is no log yet, it creates
one. Loading is where the log validates what is on disk, and where it recovers
from a crash, so it is also where the on-disk layout matters most.

## Creating a log

A new log is a directory with 1 segment file. The first sequence is
`StartSequence` (0 by default), and this only applies to a new log: for an
existing one it is ignored.

!!! warning "A new log occupies 128 MiB right away"

    Segments are preallocated to `MaxSegmentSize` by default, so even an empty
    log takes up this much disk space. Lower `Preallocate` if this is a
    problem.

A directory belongs to 1 `Log` at a time. `New` takes an exclusive lock on the
`LOCK` file in the directory, and fails with `ErrLocked` while another `Log`, in
this process or in another, holds it. `Close` releases the lock, and the system
does when the process ends.

!!! warning "A read-only log does not take the lock"

    This makes it possible to inspect a log that is in use, but a reader does not
    see anything that was not written to the file yet, and it is not stopped by a
    log that is being truncated.

!!! note "macOS"

    macOS has no `fallocate`. Segments are preallocated with `F_PREALLOCATE`
    followed by `ftruncate`, on filesystems that support it. Where it is not
    supported, the file is only extended, so no space is reserved and a full
    disk shows up during appends, instead of when the segment is created.

## Opening an existing log

`New` creates the directory when it does not exist, and fails when the path is
not a directory. Every `.bin` file in the directory is loaded as a segment, and
together they must form a contiguous range of sequence numbers. Directories,
symbolic links and other files are skipped, but should be kept elsewhere. File
names are opaque: their order comes from the start sequence in each header, so
segments should not be renamed, moved or copied.

`ReadOnly` opens the log without write access, and requires an existing log.

!!! warning "Lowering a limit can make an existing log refuse to open"

    If the files already exceed `MaxDiskSize`, loading fails with `ErrDiskFull`.
    If there are more segments than `MaxSegments`, it fails with an error
    saying so.

## Configuration

Start from `DefaultOptions()` and modify what is needed.

!!! warning "A partial `StorageOptions` is not merged with the defaults"

    The fields left out are zero, which generally means unbounded. Only a
    completely empty `StorageOptions{}` gets the defaults. Starting from
    `DefaultOptions()` avoids this.

| `StorageOptions` field | Default          | Notes                                                                                                                                     |
| ---------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `MaxDiskSize`          | 10 GiB           | Appends fail with `ErrDiskFull` at the budget. `<= 0` is unbounded.                                                                       |
| `MaxSegmentSize`       | 128 MiB          | Size of a segment file, header and end marker included. A batch must fit in an empty segment. `<= 0` is unbounded.                        |
| `MaxSegments`          | 0                | `<= 0` is unlimited. At the limit the oldest segment is recycled. `1` keeps one segment and returns `ErrSegmentFull` when it is full.     |
| `Preallocate`          | `MaxSegmentSize` | Bytes allocated when a segment is created. 128 MiB when `MaxSegmentSize` is unbounded. Negative disables. Cannot exceed `MaxSegmentSize`. |
| `CheckpointStride`     | 4096             | Records between checkpoints, 0 selects the default. Larger means fewer checkpoints, and slower seeks and crash recovery.                  |
| `SyncOnAppend`         | false            | `fdatasync` before every append returns.                                                                                                  |
| `ReadOnly`             | false            | Open without write access. Needs an existing log.                                                                                         |
| `StartSequence`        | 0                | First sequence of a new log. Ignored for an existing one.                                                                                 |
| `MaxPayloadSize`       | see below        | Largest record in bytes. Larger appends fail with `ErrPayloadTooLarge`. Negative removes the limit. Not persisted.                        |

The default `MaxPayloadSize` is `MaxSegmentSize / 64`, but at least 64 KiB, and
never more than fits in an empty segment. That is 2 MiB with the default segment
size. Without a segment size limit it is 1 MiB. A value that does not fit in an
empty segment is rejected when the log is loaded.

How these limits interact when appending is described under
[rotation and retention](./writing.md#rotation-and-retention).

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleOptions_default:start -->
## Options

### Default

This is a good starting point for most deployments: segment rotation is
bounded, retained bytes are capped, and checkpoint indexing is sparse.

Go reference: [Options](https://pkg.go.dev/stuw.net/alog#Options).

The following example shows using `DefaultOptions` as a baseline.

```go
opts := alog.DefaultOptions()
```
<!-- EXAMPLE:ExampleOptions_default:end -->

<!-- prettier-ignore-end -->

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleNewOptions:start -->
## NewOptions

Go reference: [NewOptions](https://pkg.go.dev/stuw.net/alog#NewOptions).

The following example shows constructing defaults with modifiers.

```go
opts := alog.NewOptions(
	func(o *alog.Options) {
		o.Storage.MaxSegmentSize = 8 * 1024 * 1024
	},
	func(o *alog.Options) {
		o.Storage.MaxSegments = 2
	},
)
```
<!-- EXAMPLE:ExampleNewOptions:end -->

<!-- prettier-ignore-end -->

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleOptions_singleSegmentSync:start -->
### SingleSegmentSync

This configuration keeps exactly one segment and rejects appends when full,
while `SyncOnAppend` fsyncs each write before it returns.

Go reference: [Options](https://pkg.go.dev/stuw.net/alog#Options).

The following example shows single-segment mode with synchronous durability.

```go
opts := alog.DefaultOptions()
opts.Storage.MaxSegments = 1
opts.Storage.MaxSegmentSize = 64 * 1024 * 1024
opts.Storage.SyncOnAppend = true
```
<!-- EXAMPLE:ExampleOptions_singleSegmentSync:end -->

<!-- prettier-ignore-end -->

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleOptions_boundedRotationUnboundedDisk:start -->
### BoundedRotationUnboundedDisk

This rotates by segment size but leaves total retained bytes unbounded,
which can be useful when external retention handles pruning.

Go reference: [Options](https://pkg.go.dev/stuw.net/alog#Options).

The following example shows bounded segment sizes with unbounded retained bytes.

```go
opts := alog.DefaultOptions()
opts.Storage.MaxDiskSize = 0
opts.Storage.MaxSegmentSize = 128 * 1024 * 1024
opts.Storage.MaxSegments = 0
```
<!-- EXAMPLE:ExampleOptions_boundedRotationUnboundedDisk:end -->

<!-- prettier-ignore-end -->

## Inspecting a log

A new log is empty, which shows as no first sequence, and a disk size that is
the preallocated segment.

<!-- prettier-ignore-start -->

<!-- EXAMPLE:ExampleLog_FirstSequence:start -->
## Log

### FirstSequence

Go reference: [Log.FirstSequence](https://pkg.go.dev/stuw.net/alog#Log.FirstSequence).

The following example shows sequence inspection and retained-size reporting before and after appends.

```go
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
```
<!-- EXAMPLE:ExampleLog_FirstSequence:end -->

<!-- prettier-ignore-end -->

## Segments on disk

A log is a directory of `.bin` segment files. Sequence numbers are contiguous
across segments, and the sequence of a record follows from its segment and its
position in it:

```text
sequence = segment start sequence + frame index
```

A segment is a header, followed by frames. The log always ends in an EOF
checkpoint, and the rest of the file is unwritten:

```text
segment header (72) | frame | frame | ... | checkpoint | frame | ... | EOF | unwritten
```

The 72 byte header, big-endian:

```text
offset  size  field
0       4     magic "ALOG"
4       4     version
8       32    metadata slot A
40      32    metadata slot B
```

A metadata slot holds the writable state:

```text
offset  size  field
0       8     start sequence
8       8     read offset (first readable byte, for head truncation)
16      2     slot generation
18      2     frame generation
20      2     flags
22      2     checkpoint remaining   } tracker state at the read offset,
24      4     checkpoint CRC         } so scanning can start there
28      4     slot CRC32C
```

An update writes the inactive slot with generation + 1 and syncs it. On load the
valid slot with the highest generation wins, so a torn update falls back to the
previous state. The frames themselves are described under
[writing](./writing.md#frames).

## Validation and recovery

Loading validates frame headers. Payloads are not read, which keeps loading
cheap, so a damaged payload is found when it is read, by its checksum, or by
recovery. For each segment:

1. **Header scan.** Walk the headers from the read offset, through the `mmap`,
   checking each checkpoint against the running CRC. The chain must end in a
   valid EOF checkpoint. This restores the write position, the index and the
   checkpoint state.
2. **Recovery**, only if the chain does not end in a valid EOF. Restart at the
   last valid checkpoint and verify every frame, payload included. Cut the
   segment after the last complete batch (`BatchEnd`) and write a new EOF
   checkpoint there.

A batch that was never confirmed to its caller is dropped as a whole. Recovery
is the slow path and is not optimized: torn writes should be rare, and the work
is bounded by the part of the segment since the last checkpoint.

A read-only log is not repaired on disk: the cut only happens in memory.

## Errors

| Error         | Returned when                                    |
| ------------- | ------------------------------------------------ |
| `ErrDiskFull` | The existing files already exceed the budget     |
| `ErrLocked`   | Another `Log` has the directory open for writing |
