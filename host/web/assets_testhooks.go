//go:build worklet_testhooks

package web

import _ "embed"

// Only the explicit test build exposes stall injection. Production assets are
// still used by parity, allocation, size, and soak checks.
//
//go:embed processor.js
var testProcessor []byte

func init() {
	processor = append([]byte("const CICADA_CAPTURE=false,CICADA_TEST=true;\n"), testProcessor...)
	captureProcessor = append([]byte("const CICADA_CAPTURE=true,CICADA_TEST=true;\n"), testProcessor...)
	client = append(client, []byte("\nwindow.cicadaBrowserAudio.injectStall=function(){this.node?.port.postMessage({t:'z'});};\n")...)
}
