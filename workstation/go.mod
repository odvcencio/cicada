module m31labs.dev/cicada/workstation

go 1.26

toolchain go1.26.4

tool m31labs.dev/gosx/cmd/gosx

require (
	github.com/gorilla/websocket v1.5.3
	golang.org/x/net v0.52.0
	m31labs.dev/cicada v0.0.0-00010101000000-000000000000
	m31labs.dev/gosx v0.57.5
	m31labs.dev/gosx/editor v0.19.11
)

require (
	github.com/andybalholm/brotli v1.2.4 // indirect
	github.com/chromedp/cdproto v0.0.0-20260321001828-e3e3800016bc // indirect
	github.com/chromedp/chromedp v0.15.1 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/icza/bitio v1.1.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.9 // indirect
	github.com/mewkiz/flac v1.0.14 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	github.com/odvcencio/corkscrewdb v0.2.0 // indirect
	github.com/odvcencio/gotreesitter v0.54.0 // indirect
	github.com/odvcencio/turboquant v0.1.3 // indirect
	github.com/orisano/pixelmatch v0.0.0-20220722002657-fb0b55479cde // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/image v0.38.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
	m31labs.dev/eos v0.1.4 // indirect
	m31labs.dev/mll v0.1.0 // indirect
	m31labs.dev/prism v0.1.3 // indirect
	m31labs.dev/selena v0.5.2 // indirect
	m31labs.dev/turboquant v0.2.1 // indirect
)

replace m31labs.dev/cicada => ..

replace m31labs.dev/tymbal => github.com/odvcencio/tymbal v0.0.0-20260929091837-23968cefb0c6
