// cicada-demo serves the immutable browser demo without native Studio APIs.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"m31labs.dev/cicada/host/demobrowser"
	"m31labs.dev/cicada/host/demoweb"
	"m31labs.dev/cicada/host/web"
)

var revision string

func main() {
	bundle := flag.String("bundle", "build/demo", "directory containing the three browser runtime assets")
	listen := flag.String("listen", "127.0.0.1:8170", "HTTP listen address")
	host := flag.String("host", "", "HTTP Host; defaults to the loopback listener or the public demo host")
	flag.Parse()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(*bundle, name))
		if err != nil {
			log.Fatal(err)
		}
		return data
	}
	kernel, bridge, runtime := read("kernel.wasm"), read("demo.wasm"), read("wasm_exec.js")
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	if *host == "" {
		if address, ok := listener.Addr().(*net.TCPAddr); ok && address.IP.IsLoopback() {
			*host = listener.Addr().String()
		}
	}
	handler, err := demoweb.NewHandler(demobrowser.Build(revision, *host, kernel, bridge, runtime, web.Processor()))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("http://%s\n", listener.Addr())
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.Serve(listener))
}
