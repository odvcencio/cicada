// Package edit is the Cicada edit service core. It turns typed intents into
// source patches: Apply takes the current source, an attributed envelope and
// injected options, and returns the candidate source, any auxiliary files, a
// history label and a response map.
//
// The package is pure. It imports only notation and pure helpers, and it must
// build for GOOS=js GOARCH=wasm. Anything that needs the project compiler, the
// renderer or the file system is injected through Options. purity_test.go
// enforces both rules.
package edit
