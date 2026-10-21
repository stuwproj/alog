# alog

`alog` is a disk-backed append-only log for Go. It stores byte records under sequence numbers in segments, with atomic batch appends, configurable durability, and retention controls.

Its design targets high read and write performance while accommodating a broad range of common append-only logging use cases.

It supports sequential scanning, sequence-based seeks, prefix and suffix truncation, and grouped writes from concurrent producers.

## Documentation

- [Overview and use cases](https://docs.stuw.net/alog/latest/)
- [Quickstart](https://docs.stuw.net/alog/latest/quickstart/)
- [Go API reference](https://pkg.go.dev/stuw.net/alog)

## Community

Questions and feedback: [Stuw Discussions](https://github.com/stuwproj/docs/discussions). Report defects in [alog issues](https://github.com/stuwproj/alog/issues).

## License

MIT
