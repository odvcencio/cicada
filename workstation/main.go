package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Cicada Studio:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("cicada-workstation", flag.ContinueOnError)
	address := flags.String("listen", defaultAddress(), "GoSX workstation listen address")
	service := flags.String("backend", "", "Tymbal service origin (started by cicada studio)")
	watchParent := flags.Bool("parent-watch", false, "exit when the parent closes stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || (host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
		return fmt.Errorf("listen address must be loopback host:port")
	}
	var b *backend
	// A framework build has no open score or audio device. Keep the workspace
	// dynamic and private while allowing GoSX's export readiness probe to run.
	if os.Getenv("GOSX_STATIC_EXPORT") != "1" || *service != "" {
		b, err = newBackend(*service)
		if err != nil {
			return err
		}
	}
	handler, err := newApp(b)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *watchParent {
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); stop() }()
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	fmt.Printf("Cicada Studio: http://%s/\n", listener.Addr().String())
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func defaultAddress() string {
	if address := os.Getenv("GOSX_LISTEN_ADDR"); address != "" {
		return address
	}
	if port := os.Getenv("PORT"); port != "" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return "127.0.0.1:0"
}
