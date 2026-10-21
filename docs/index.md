---
title: Home
---

# alog

`alog` is a disk-backed append-only log for Go. It stores opaque byte records in
segments and assigns consecutive `uint64` sequence numbers. Each append call
writes an atomic batch: after crash recovery, the complete batch is retained
or none of it is.

Its design targets high read and write performance while accommodating a
broad range of common append-only logging use cases.

## Use cases

- **Write-ahead logs:** persist ordered changes for replay and recovery.
- **Consensus log storage:** retain and truncate sequence-addressed entries for
  integrations with consensus implementations.
- **Event journals:** append records and replay them sequentially from a saved
  sequence number.
- **Bounded record histories:** retain recent records by recycling older
  segments when configured to do so.

Applications define the meaning and encoding of records. `alog` handles local
storage, sequence assignment, and retrieval.

## Capabilities and trade-offs

- Append individual records or atomic batches; optionally sync before returning.
- Scan records in order, seek by sequence, or read bounded ranges.
- Use `Writer` to group concurrent producers into batches.
- Configure segment sizes, disk budgets, recycling, and truncation.
- Recover incomplete writes after a crash using checkpoints and checksums.

Appends are **not durable by default**. Enable `SyncOnAppend` or call `Sync`
when durability is required. A log is not safe for concurrent direct writes;
use `Writer` for multiple producers. The default disk budget is 10 GiB, and
reaching it rejects appends unless retention is configured to reclaim space.
See [writing](./writing.md) for the precise guarantees.

## Getting started

Follow the [quickstart](./quickstart.md) to create, append, sync, scan, and
reopen a log.

## Guides

- [Loading](./loading.md): creation, configuration, segment layout, and recovery.
- [Writing](./writing.md): appends, durability, retention, truncation, and
  concurrent producers.
- [Scanning](./scanning.md): reading, seeking, ranges, and following new records.

See also the [Go API reference](https://pkg.go.dev/stuw.net/alog).

## Compatibility

`alog` is supported on 64-bit Linux. It uses `pwritev`, `fdatasync`,
`fallocate`, and `mmap`, and requires a filesystem supporting `fallocate`.

macOS is supported for local development, with differences in preallocation
and durability described in [loading](./loading.md#creating-a-log) and
[writing](./writing.md#durability).

Questions and feedback belong in [Stuw Discussions](https://github.com/stuwproj/docs/discussions).
Report defects in [alog issues](https://github.com/stuwproj/alog/issues).

## License

MIT
