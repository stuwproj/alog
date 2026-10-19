---
title: Home
---

# alog

A fast, general-purpose, append-only log for Go.

`alog` stores opaque byte records under increasing sequence numbers, in segment
files. A single append call writes a batch of records, and after a crash this
batch is either completely present or not present at all.

These are the 3 things it does:

- [Loading](./loading.md): creating and opening a log, configuration, the layout
  on disk, and recovery after a crash.
- [Scanning](./scanning.md): reading records back, seeking and ranges.
- [Writing](./writing.md): appending, durability, retention, truncation and the
  concurrent `Writer`.

[Components](./components.md) shows how these are put together. Things that are
easy to get wrong are called out in place, as warnings.

## Compatibility

`alog` is supported on 64-bit Linux. It uses `pwritev`, `fdatasync`, `fallocate`
and `mmap`, and requires a filesystem that supports `fallocate`.

macOS is supported for local development. Performance will be worse and behavior
slightly deviates, which is noted in place, under
[creating a log](./loading.md#creating-a-log) and
[durability](./writing.md#durability).

## License

MIT
