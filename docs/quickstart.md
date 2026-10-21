# Quickstart

Install the module in a Go project:

```bash
go get stuw.net/alog
```

The following program creates a log, appends two records, syncs them to
durable storage, closes the log, and reopens it to read them back.

```go
package main

import (
    "fmt"
    "log"
    "os"

    "stuw.net/alog"
)

func main() {
    dir, err := os.MkdirTemp("", "alog-quickstart-")
    if err != nil {
        log.Fatal(err)
    }
    defer os.RemoveAll(dir)

    opts := alog.DefaultOptions()
    l, err := alog.New(dir, opts)
    if err != nil {
        log.Fatal(err)
    }

    if _, err := l.Append([]byte("first"), []byte("second")); err != nil {
        log.Fatal(err)
    }
    if err := l.Sync(); err != nil {
        log.Fatal(err)
    }
    if err := l.Close(); err != nil {
        log.Fatal(err)
    }

    l, err = alog.New(dir, opts)
    if err != nil {
        log.Fatal(err)
    }
    defer l.Close()

    err = alog.RangeFrom(l, 0, 0, func(seq uint64, data []byte) error {
        fmt.Printf("%d: %s\n", seq, data)
        return nil
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

Expected output:

```text
0: first
1: second
```

`Append` returns the last sequence in the batch. `Sync` makes previously
appended records durable; appends are not synced by default. The default
configuration preallocates a 128 MiB segment and caps disk usage at 10 GiB.

Continue with [writing](./writing.md) for durability, concurrent producers,
and retention; [scanning](./scanning.md) for seeks and ranges; and
[loading](./loading.md) for configuration and recovery.
