//go:build js && wasm

// Browser qualification bridge only. This is not a Studio UI or hosted demo.
package main

import (
	"fmt"
	"syscall/js"

	"m31labs.dev/cicada/host/browsercontrol"
	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const source = `tempo 120
key a minor
track bass acid { cutoff = 620Hz }
track drums drums {}
instrument tone {
 voice mono {
  let osc = saw(pitch)
  out = osc * env(gate, 50ms)
 }
}
track lead tone {}
pattern rest { . . . . }
pattern drumrest drums { bd: .... }
pattern leadrest notes { . . . . }
scene quiet {
 bass = rest
 drums = drumrest
 lead = leadrest
}
song { quiet*1 }
`

func projectForTest() *project.Project {
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			panic(d.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			panic(d.Message)
		}
	}
	if err := project.ValidateProject(p); err != nil {
		panic(err)
	}
	return p
}

func main() {
	p := projectForTest()
	var controller *browsercontrol.Controller
	var lifecycle *browsercontrol.Lifecycle
	functions := make([]js.Func, 0)
	api := js.Global().Get("Object").New()
	bind := func(name string, call func([]js.Value) any) {
		fn := js.FuncOf(func(_ js.Value, args []js.Value) any { return call(args) })
		functions = append(functions, fn)
		api.Set(name, fn)
	}
	result := func(err error) any {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	bind("image", func(args []js.Value) any {
		cfg, err := project.CompileEngine(p, args[0].Int(), 128)
		if err != nil {
			panic(err)
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			panic(err)
		}
		out := js.Global().Get("Uint8Array").New(len(image))
		js.CopyBytesToJS(out, image)
		return out
	})
	bind("bind", func(args []js.Value) any {
		if lifecycle != nil {
			lifecycle.Close()
		}
		cfg, err := project.CompileEngine(p, args[1].Get("sampleRate").Int(), 128)
		if err != nil {
			return result(err)
		}
		controller, err = browsercontrol.New(p, cfg, demopolicy.PublicDemo(), browsercontrol.WorkletSender(args[0].Get("port")))
		if err != nil {
			return result(err)
		}
		lifecycle = browsercontrol.BindLifecycle(controller, args[1], args[2], func(err error) { js.Global().Set("qualificationError", err.Error()); args[0].Call("disconnect") })
		return ""
	})
	bind("play", func([]js.Value) any { return result(controller.Play()) })
	bind("resetReady", func([]js.Value) any { controller.KernelResetReady(); return "" })
	bind("stop", func([]js.Value) any { return result(controller.Panic()) })
	bind("down", func(a []js.Value) any {
		return result(controller.Down(a[0].String(), a[1].String(), a[2].Int(), a[3].Int(), a[4].Bool()))
	})
	bind("up", func(a []js.Value) any { return result(controller.Up(a[0].String())) })
	bind("param", func(a []js.Value) any { v := a[1].Float(); return result(controller.SetParam(a[0].String(), &v)) })
	bind("export", func(a []js.Value) any {
		return result(demopolicy.PublicDemo().Dispatch(demopolicy.Action(a[0].String()), func() error { return fmt.Errorf("export side effect must never execute") }))
	})
	bind("close", func([]js.Value) any { lifecycle.Close(); return "" })
	js.Global().Set("cicadaQualification", api)
	select {}
}
