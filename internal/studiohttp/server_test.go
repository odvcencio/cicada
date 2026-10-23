package studiohttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T, ctx context.Context, handler http.Handler) (*http.Server, string, <-chan struct{}) {
	t.Helper()
	s := New(ctx, handler)
	accepted := make(chan struct{}, 1)
	state := s.ConnState
	s.ConnState = func(conn net.Conn, next http.ConnState) {
		state(conn, next)
		if next == http.StateNew {
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- s.Serve(l) }()
	t.Cleanup(func() {
		_ = s.Close()
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	})
	return s, l.Addr().String(), accepted
}

func shutdown(t *testing.T, s *http.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestShutdownClosesUnfinishedBrowserConnections(t *testing.T) {
	for _, headers := range []string{"", "GET / HTTP/1.1\r\nHost: localhost\r\n"} {
		t.Run(map[bool]string{true: "preconnect", false: "partial-headers"}[headers == ""], func(t *testing.T) {
			called := make(chan struct{}, 1)
			s, address, accepted := start(t, context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called <- struct{}{} }))
			conn, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := io.WriteString(conn, headers); err != nil {
				t.Fatal(err)
			}
			<-accepted // Make the pending connection authoritative before stopping.
			shutdown(t, s)
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); err == nil {
				t.Fatal("unfinished connection survived shutdown")
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("unfinished connection was not closed")
			}
			select {
			case <-called:
				t.Fatal("partial request entered a handler")
			default:
			}
		})
	}
}

func TestShutdownClosesAConnectionAnnouncedAfterStopping(t *testing.T) {
	s, _, _ := start(t, context.Background(), http.NotFoundHandler())
	pending, pendingPeer := net.Pipe()
	defer pending.Close()
	defer pendingPeer.Close()
	s.ConnState(pending, http.StateNew)
	shutdown(t, s)
	// Shutdown callbacks run asynchronously. Wait for this one to finish
	// closing an already pending connection before announcing a late accept.
	_ = pendingPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := pendingPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("pending connection was not closed: %v", err)
	}
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	s.ConnState(conn, http.StateNew)
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("late connection was not closed: %v", err)
	}
}

func TestShutdownLetsAnActiveWriteFinish(t *testing.T) {
	started, finish, saved := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	s, address, accepted := start(t, context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read active write: %v", err)
		}
		saved <- string(body)
		_, _ = io.WriteString(w, "saved")
	}))
	result := make(chan error, 1)
	go func() {
		response, err := http.Post("http://"+address, "text/plain", strings.NewReader("musical edit"))
		if err == nil {
			defer response.Body.Close()
			var body []byte
			body, err = io.ReadAll(response.Body)
			if err == nil && string(body) != "saved" {
				err = errors.New("active write lost its acknowledgment")
			}
		}
		result <- err
	}()
	<-started
	<-accepted
	pending, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	<-accepted
	stopped := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { stopped <- s.Shutdown(ctx) }()
	_ = pending.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := pending.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("shutdown retained a preconnection alongside an active write: %v", err)
	}
	// An active handler must still own its connection while shutdown runs.
	select {
	case err := <-stopped:
		t.Fatalf("shutdown did not wait for an active write: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := <-saved; got != "musical edit" {
		t.Fatalf("saved %q", got)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownCancelsStreamingRequestsWithTheApplication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	s, address, _ := start(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "stream ready\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	response, err := http.Get("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-started
	cancel()
	shutdown(t, s)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("request retained the canceled application")
	}
}

func TestShutdownStillReportsAnActiveHandlerDeadline(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	s, address, _ := start(t, context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-finish
	}))
	defer close(finish)
	go func() {
		response, err := http.Get("http://" + address)
		if err == nil {
			response.Body.Close()
		}
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stuck active handler error = %v", err)
	}
}
