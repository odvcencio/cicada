package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/host/midi"
)

type testMIDIStream struct {
	events chan midi.Event
	errors chan error
	closed chan struct{}
	once   sync.Once
}

func (s *testMIDIStream) Events() <-chan midi.Event { return s.events }
func (s *testMIDIStream) Errors() <-chan error      { return s.errors }
func (s *testMIDIStream) Close() error              { s.once.Do(func() { close(s.closed) }); return nil }
func newTestMIDIStream() *testMIDIStream {
	return &testMIDIStream{make(chan midi.Event, 2), make(chan error, 1), make(chan struct{}), sync.Once{}}
}

func TestNativeMIDIStreamChecksOriginAndClosesOnDisconnect(t *testing.T) {
	var control studioMIDIInput
	var opens atomic.Int32
	opened := make(chan *testMIDIStream, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		control.serve(w, r, func(context.Context) (midi.Stream, error) {
			opens.Add(1)
			s := newTestMIDIStream()
			s.events <- midi.Event{Type: "devices"}
			opened <- s
			return s, nil
		}, nil)
	}))
	defer server.Close()
	post := func(ctx context.Context, origin string) *http.Response {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
		r.Header.Set("Origin", origin)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := post(context.Background(), "https://other.example")
	response.Body.Close()
	if response.StatusCode != 403 || opens.Load() != 0 {
		t.Fatal("foreign origin opened MIDI")
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		response = post(ctx, server.URL)
		if response.StatusCode != 200 {
			t.Fatal(response.StatusCode)
		}
		var event midi.Event
		if err := json.NewDecoder(response.Body).Decode(&event); err != nil || event.Type != "devices" {
			t.Fatalf("stream start: %+v %v", event, err)
		}
		stream := <-opened
		other := post(context.Background(), server.URL)
		other.Body.Close()
		if other.StatusCode != 409 || opens.Load() != int32(attempt+1) {
			t.Fatal("duplicate stream opened a native port")
		}
		cancel()
		response.Body.Close()
		select {
		case <-stream.closed:
		case <-time.After(time.Second):
			t.Fatal("disconnect retained the MIDI port")
		}
		deadline := time.Now().Add(time.Second)
		for control.connected.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if control.connected.Load() {
			t.Fatal("disconnect retained the input lease")
		}
	}
}

func TestNativeMIDIStreamFailsClosedOnOverflow(t *testing.T) {
	var control studioMIDIInput
	stream := newTestMIDIStream()
	stream.errors <- midi.ErrOverflow
	stream.events <- midi.Event{Type: "message", Data: [3]uint8{144, 60, 100}, Length: 3}
	w := httptest.NewRecorder()
	control.serve(w, httptest.NewRequest(http.MethodPost, "http://localhost/api/midi/input", nil), func(context.Context) (midi.Stream, error) { return stream, nil }, nil)
	var event midi.Event
	if err := json.Unmarshal(w.Body.Bytes(), &event); err != nil || event.Type != "error" || event.Message != midi.ErrOverflow.Error() {
		t.Fatalf("overflow emitted stale input: %s %v", w.Body, err)
	}
	select {
	case <-stream.closed:
	default:
		t.Fatal("overflow retained a native port")
	}
}

func TestNativeMIDIUnavailableReleasesConnectionClaim(t *testing.T) {
	var control studioMIDIInput
	for _, failure := range []error{midi.ErrUnsupported, errors.New("MIDI service unavailable")} {
		w := httptest.NewRecorder()
		control.serve(w, httptest.NewRequest(http.MethodPost, "http://localhost/api/midi/input", nil), func(context.Context) (midi.Stream, error) { return nil, failure }, nil)
		want := http.StatusServiceUnavailable
		if errors.Is(failure, midi.ErrUnsupported) {
			want = http.StatusNotImplemented
		}
		if w.Code != want {
			t.Fatalf("status = %d, want %d", w.Code, want)
		}
		if control.connected.Load() {
			t.Fatal("failed open retained the connection claim")
		}
	}
}
