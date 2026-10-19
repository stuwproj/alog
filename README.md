# alog

`alog` ("a-log") is a fast, general-purpose, append-only log for Go.

## Design 

### Concerns

`alog` has the following design concerns, in order of importance:

- correctness
- crash safety
- bounded disk usage
- durable append throughput
- gc pressure
- scan throughput
- non-durable append throughput
- resource economy (FDs, memory, and to a lesser degree CPU)
- multi-use case (WAL, archiving, etc)

### Key Properties

- monotonic sequence numbers
- load-time header validation
- frame header and optional payload integrity checks
- vectored (durable and non-durable) appends
- memory mapped range scans
- economical file descriptor usage
- logical head truncation
- heap allocation optimized

### Notable Options

- implicit write durability
- indexed logical offset (sequence number) seeks
- pre-allocation and segment recycling (frame generations, logical tail truncation)
- disk-usage guarantees and automatic segment reaping
- concurrent appends with opportunistic grouping

### Performance Characteristics

- scans/checks headers at load time in the order of 10s of millions records/s (`mmap`-ed)
- non-durable sequential single appends in the order of 100s of thousands records/s (`pwritev`)
- sequential batching scales near-horizontally
- durable sequential single appends around 2.5K records/s (`fdatasync`)
- durable concurrent appends in the order of 100s of thousands records/s (opportunistic commit)

## Usage

Rendered GoDoc is available at [pkg.go.dev](https://pkg.go.dev/stuw.net/alog).

## Related

- [`stuwproj/frame`](https://github.com/stuwproj/frame): binary framing utilities used by `alog`.

## Support

Questions, feedback, ideas are welcome at [GitHub Discussions](https://github.com/stuwproj/alog/discussions).

## Bugs

Reporting bugs is highly encouraged. If you find a logical bug, a crash scenario that `alog` should be 
able to mitigate, or performance degradation scenarios, please create a GitHub ticket. PRs are also
more than welcome.

## License

MIT
