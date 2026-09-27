module m31labs.dev/cicada

go 1.25.0

require (
	github.com/andybalholm/brotli v1.2.4
	github.com/coder/websocket v1.8.15
	github.com/ebitengine/oto/v3 v3.5.1
	github.com/odvcencio/gotreesitter v0.54.0
	github.com/tetratelabs/wazero v1.12.0
)

require (
	github.com/ebitengine/purego v0.11.0 // indirect
	github.com/jfreymuth/pulse v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
	m31labs.dev/tymbal v0.0.0-20260927121518-a296366dc975
)

replace m31labs.dev/tymbal => github.com/odvcencio/tymbal v0.0.0-20260927121518-a296366dc975
