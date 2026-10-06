// Package web contains the small browser host assets used by Cicada Studio.
package web

import _ "embed"

// Run make build-worklets to rebuild both profiles from processor.js.
// processor.min.js is Terser 5.39.0 output (ECMA 2020, 5 compression passes, unsafe compression, top-level mangling).
//
//go:embed processor.min.js
var processor []byte

//go:embed client.js
var client []byte

func Processor() []byte { return processor }
func Client() []byte    { return client }

//go:embed capture.js
var captureAdapter []byte

//go:embed processor-capture.min.js
var captureProcessor []byte

//go:embed capture-worker.js
var captureWorker []byte

//go:embed capture-client.js
var captureClient []byte

func CaptureAdapter() []byte   { return captureAdapter }
func CaptureProcessor() []byte { return captureProcessor }
func CaptureWorker() []byte    { return captureWorker }
func CaptureClient() []byte    { return captureClient }

// Optional sample instrument host; serve both modules at sibling URLs.
//
//go:embed instrument-pack.js
var instrumentPack []byte

//go:embed sampler-processor.js
var samplerProcessor []byte

func InstrumentPack() []byte   { return instrumentPack }
func SamplerProcessor() []byte { return samplerProcessor }
