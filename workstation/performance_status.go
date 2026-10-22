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

// GoSX projects enhanced form errors into a status inside the submitted form.
// A span keeps an empty status from adding a paragraph's margins to the grid.
func gridFormStatus(ctx *server.Context, pattern, operation string) gosx.Node {
	message := ""
	state := action.States(ctx.Request)["pattern"]
	if !state.OK() && state.Value("pattern") == pattern && state.Value("action") == operation {
		message = state.Message()
	}
	return gosx.El("span", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status"), gosx.Attr("style", "color:var(--coral);max-width:100%")), gosx.Text(message))
}
