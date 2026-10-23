package audiobackend

import (
	"context"
	"errors"
	"testing"
)

type permissionBackend struct {
	Backend
	queries, requests int
	access            InputAccess
	err               error
}

func (*permissionBackend) CaptureSupported() bool { return true }
func (b *permissionBackend) InputAccess() (InputAccess, error) {
	b.queries++
	return b.access, b.err
}
func (b *permissionBackend) RequestInputAccess(context.Context) error {
	b.requests++
	return b.err
}

func TestInputPermissionQueryNeverRequestsAndCancellationPreservesState(t *testing.T) {
	denied := errors.New("native permission denied")
	b := &permissionBackend{access: InputAccessDenied, err: denied}
	if access, err := QueryInputAccess(b); access != InputAccessDenied || !errors.Is(err, denied) || b.requests != 0 {
		t.Fatalf("query requested input: %q %v %+v", access, err, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RequestInputAccess(ctx, b); !errors.Is(err, context.Canceled) || b.requests != 0 {
		t.Fatalf("canceled request reached native authorization: %v %+v", err, b)
	}
	if err := RequestInputAccess(context.Background(), b); !errors.Is(err, denied) || b.requests != 1 {
		t.Fatalf("native denial lost: %v %+v", err, b)
	}
	for _, name := range []Name{Oto, Null} {
		output, err := New(name)
		if err != nil {
			t.Fatal(err)
		}
		if access, err := QueryInputAccess(output); err != nil || access != InputAccessUnavailable {
			t.Fatalf("%s: %q %v", name, access, err)
		}
		if err := RequestInputAccess(context.Background(), output); err == nil {
			t.Fatalf("%s accepted input", name)
		}
	}
}
