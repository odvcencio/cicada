module m31labs.dev/cicada/desktop

go 1.26.0

require m31labs.dev/gosx v0.57.16

// Pin the published Darwin backend until it is available in an upstream release.
replace m31labs.dev/gosx => github.com/jvcencio/gosx v0.0.0-20261019011808-5029619202a3

require golang.org/x/sys v0.43.0 // indirect
