module m31labs.dev/cicada/desktop

go 1.26.0

require (
	github.com/ebitengine/purego v0.11.2
	m31labs.dev/gosx v0.57.16
)

// Pin the published Darwin backend until it is available in an upstream release.
replace m31labs.dev/gosx => github.com/odvcencio/gosx v0.57.17-0.20261018233127-670b577ea5bc

require golang.org/x/sys v0.43.0 // indirect
