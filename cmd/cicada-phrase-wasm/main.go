// cicada-phrase-wasm exercises the seeded phrase generator on the same TinyGo
// target as the audio kernel. Generation runs outside the audio callback.
package main

import (
	"encoding/json"
	"unsafe"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/phrase"
)

const maxParamsBytes = 4096

var paramsBuffer []byte
var resultBuffer []byte

//go:wasmexport cicada_phrase_params_alloc
func paramsAlloc(length uint32) uint32 {
	if length == 0 || length > maxParamsBytes {
		return 0
	}
	paramsBuffer = make([]byte, length)
	return uint32(uintptr(unsafe.Pointer(&paramsBuffer[0])))
}

//go:wasmexport cicada_phrase_generate
func generate() uint32 {
	resultBuffer = nil
	var params phrase.Params
	if err := json.Unmarshal(paramsBuffer, &params); err != nil {
		resultBuffer = []byte(err.Error())
		return 1
	}
	result, err := phrase.Generate(params)
	return writeResult(result, err)
}

type mutationRequest struct {
	Params     phrase.Params
	Base       seq.Pattern
	Ops        []phrase.Op
	Locked     uint64
	Generation uint64
}

//go:wasmexport cicada_phrase_mutate
func mutate() uint32 {
	resultBuffer = nil
	var request mutationRequest
	if err := json.Unmarshal(paramsBuffer, &request); err != nil {
		resultBuffer = []byte(err.Error())
		return 1
	}
	result, err := phrase.Mutate(request.Params, request.Base, request.Ops, request.Locked)
	return writeResult(result, err)
}

//go:wasmexport cicada_phrase_evolve
func evolve() uint32 {
	resultBuffer = nil
	var request mutationRequest
	if err := json.Unmarshal(paramsBuffer, &request); err != nil {
		resultBuffer = []byte(err.Error())
		return 1
	}
	result, err := phrase.Evolve(request.Params, request.Base, request.Generation, request.Locked)
	return writeResult(result, err)
}

func writeResult(result phrase.Result, err error) uint32 {
	if err != nil {
		resultBuffer = []byte(err.Error())
		return 2
	}
	trace, err := json.Marshal(result.Trace)
	if err != nil {
		resultBuffer = []byte(err.Error())
		return 3
	}
	resultBuffer = make([]byte, 0, len(result.Notation)+1+len(trace))
	resultBuffer = append(resultBuffer, result.Notation...)
	resultBuffer = append(resultBuffer, 0)
	resultBuffer = append(resultBuffer, trace...)
	return 0
}

//go:wasmexport cicada_phrase_result_ptr
func resultPtr() uint32 {
	if len(resultBuffer) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&resultBuffer[0])))
}

//go:wasmexport cicada_phrase_result_len
func resultLen() uint32 { return uint32(len(resultBuffer)) }

func main() {}
