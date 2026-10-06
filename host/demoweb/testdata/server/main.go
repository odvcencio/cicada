// Static browser qualification fixture; never deployed as Cicada Studio.
package main

import (
	"fmt"
	"m31labs.dev/cicada/host/demoweb"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: server <repo> <wasm_exec.js>")
	}
	repo := os.Args[1]
	assets := make(map[string]demoweb.Asset)
	for _, entry := range []struct{ url, path, mime string }{
		{"/", filepath.Join(repo, "scripts/demo-browser/index.html"), "text/html; charset=utf-8"},
		{"/assets/check.js", filepath.Join(repo, "scripts/demo-browser/page.js"), "application/javascript"},
		{"/assets/wasm_exec.js", os.Args[2], "application/javascript"},
		{"/assets/bridge.wasm", filepath.Join(repo, "build/demo-control-bridge.wasm"), "application/wasm"},
		{"/assets/kernel.wasm", filepath.Join(repo, "build/cicada-kernel.wasm"), "application/wasm"},
		{"/audio/processor.js", filepath.Join(repo, "host/web/processor.min.js"), "application/javascript"},
	} {
		data, err := os.ReadFile(entry.path)
		if err != nil {
			panic(err)
		}
		assets[entry.url] = demoweb.Asset{ContentType: entry.mime, Bytes: data}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	handler, err := demoweb.NewHandler(demoweb.Build{Revision: "954826cfd34f6f6ec3c1fde9f03c0dd0c111ea03", Host: listener.Addr().String(), Assets: assets})
	if err != nil {
		panic(err)
	}
	fmt.Println("http://" + listener.Addr().String())
	if err := http.Serve(listener, handler); err != nil {
		panic(err)
	}
}
