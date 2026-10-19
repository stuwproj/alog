module stuw.net/alog

go 1.26.9

retract v1.0.0 // don't use v1.0.0

require (
	github.com/fuweid/go-dmflakey v0.0.0-20240326101426-9fa9d5c2ac1c
	stuw.net/frame v0.4.0
	github.com/stretchr/testify v1.12.1
	go.uber.org/goleak v1.3.0
	golang.org/x/sys v0.14.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	stuw.net/go-tools v0.0.4 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/yuin/goldmark v1.7.13 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

tool (
	stuw.net/go-tools/cmd/bench
	stuw.net/go-tools/cmd/docs
	stuw.net/go-tools/cmd/fuzz
	stuw.net/go-tools/cmd/lint
	stuw.net/go-tools/cmd/mut
	stuw.net/go-tools/cmd/prof
	stuw.net/go-tools/cmd/release
	stuw.net/go-tools/cmd/test
	stuw.net/go-tools/cmd/tools
)

replace github.com/fuweid/go-dmflakey => github.com/johnknl/go-dmflakey v0.0.0-20240326101426-9fa9d5c2ac1c
