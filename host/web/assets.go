// Package web contains the small browser host assets used by Cicada Studio.
package web

import _ "embed"

// processor.min.js is Terser 5.39.0 output (ECMA 2020, 5 compression passes, unsafe compression, top-level mangling).
//
//go:embed processor.min.js
var processor []byte

//go:embed client.js
var client []byte

func Processor() []byte { return processor }
func Client() []byte    { return client }
