//go:build js && wasm

// Local research audition bridge. Intentionally independent of the instrument ABI.
package main

import (
	"encoding/binary"
	"math"
	"syscall/js"

	"m31labs.dev/cicada/experimental/expressive"
)

func main() {
	var voice expressive.Voice
	var packed [4096 * 4]byte
	var callbacks []js.Func // Keep callbacks alive for the page lifetime.
	api := js.Global().Get("Object").New()
	bind := func(name string, f func(js.Value, []js.Value) any) {
		callback := js.FuncOf(f)
		callbacks = append(callbacks, callback)
		api.Set(name, callback)
	}
	bind("init", func(_ js.Value, args []js.Value) any {
		rate := args[0].Int()
		switch args[1].String() {
		case "brass":
			voice = expressive.NewBrass(rate)
		case "guitar":
			voice = expressive.NewGuitar(rate)
		default:
			voice = expressive.NewBow(rate)
		}
		return nil
	})
	bind("noteOn", func(_ js.Value, args []js.Value) any {
		if voice != nil {
			voice.NoteOn(args[0].Float(), args[1].Float())
		}
		return nil
	})
	bind("noteOff", func(_ js.Value, _ []js.Value) any {
		if voice != nil {
			voice.NoteOff()
		}
		return nil
	})
	bind("expression", func(_ js.Value, args []js.Value) any {
		if voice != nil {
			e := args[0]
			voice.SetExpression(expressive.Expression{
				PitchHz: e.Get("pitch").Float(), Pressure: e.Get("pressure").Float(),
				Position: e.Get("position").Float(), Brightness: e.Get("brightness").Float(),
				Vibrato: e.Get("vibrato").Float(), Drive: e.Get("drive").Float(), Damping: e.Get("damping").Float(),
			})
		}
		return nil
	})
	// Caller supplies an existing Uint8Array view over a Float32Array. Copy a
	// whole block across the JS/Go boundary instead of invoking Go per sample.
	bind("render", func(_ js.Value, args []js.Value) any {
		n := args[0].Get("byteLength").Int() / 4
		if n > len(packed)/4 {
			return false
		}
		for i := 0; i < n; i++ {
			x := 0.0
			if voice != nil {
				x = voice.Next()
			}
			if math.IsNaN(x) || math.IsInf(x, 0) {
				x = 0
			}
			binary.LittleEndian.PutUint32(packed[i*4:], math.Float32bits(float32(x)))
		}
		js.CopyBytesToJS(args[0], packed[:n*4])
		return true
	})
	js.Global().Set("cicadaExpressive", api)
	select {}
}
