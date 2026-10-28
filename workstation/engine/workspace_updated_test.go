package main

import (
	"reflect"
	"testing"
)

func TestWorkspaceUpdatedRunsHooksInRegistrationOrder(t *testing.T) {
	var hooks workspaceUpdatedHooks
	var calls []string
	hooks.add(func() { calls = append(calls, "curve") })
	hooks.add(func() { calls = append(calls, "vocals") })
	hooks.run()
	hooks.run()
	if want := []string{"curve", "vocals", "curve", "vocals"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestWorkspaceUpdatedRemoveStopsCallsAndIsIdempotent(t *testing.T) {
	var hooks workspaceUpdatedHooks
	var calls []string
	removeFirst := hooks.add(func() { calls = append(calls, "first") })
	hooks.add(func() { calls = append(calls, "second") })
	hooks.run()
	removeFirst()
	removeFirst()
	hooks.run()
	if want := []string{"first", "second", "second"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// An engine that is disposed while a projection is being applied must not be
// called afterwards, even if the dispose came from an earlier hook.
func TestWorkspaceUpdatedSkipsAHookRemovedDuringRun(t *testing.T) {
	var hooks workspaceUpdatedHooks
	var calls []string
	var removeSecond func()
	hooks.add(func() {
		calls = append(calls, "first")
		removeSecond()
	})
	removeSecond = hooks.add(func() { calls = append(calls, "second") })
	hooks.add(func() { calls = append(calls, "third") })
	hooks.run()
	if want := []string{"first", "third"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// A hook may register another hook. It is not called in the pass that added it.
func TestWorkspaceUpdatedHookAddedDuringRunWaitsForTheNextPass(t *testing.T) {
	var hooks workspaceUpdatedHooks
	var calls []string
	added := false
	hooks.add(func() {
		calls = append(calls, "outer")
		if !added {
			added = true
			hooks.add(func() { calls = append(calls, "inner") })
		}
	})
	hooks.run()
	hooks.run()
	if want := []string{"outer", "outer", "inner"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
