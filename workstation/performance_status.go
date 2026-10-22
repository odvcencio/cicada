package main

import (
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func performanceFormStatus(ctx *server.Context, pattern, operation string) gosx.Node {
	message := ""
	state := action.States(ctx.Request)["pattern"]
	if !state.OK() && state.Value("pattern") == pattern && state.Value("action") == operation {
		message = state.Message()
	}
	return gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(message))
}
