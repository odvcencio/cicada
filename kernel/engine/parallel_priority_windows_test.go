//go:build !tinygo && !js && windows

package engine

import (
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestParallelPriorityFakeAPILifecycleAndFallback(t *testing.T) {
	for _, test := range []struct {
		name               string
		register, priority bool
		want               string
	}{
		{"critical", true, true, "MMCSS Pro Audio critical"},
		{"default-relative-priority", true, false, "MMCSS Pro Audio"},
		{"service-unavailable", false, false, "normal OS scheduling; MMCSS unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			thread := windows.GetCurrentThreadId()
			var registered, prioritized, reverted int
			api := parallelPriorityAPI{
				register: func(task string) uintptr {
					registered++
					if task != "Pro Audio" || windows.GetCurrentThreadId() != thread {
						t.Fatal("incorrect registration task/thread")
					}
					if test.register {
						return 17
					}
					return 0
				},
				priority: func(handle uintptr, relative int32) bool {
					prioritized++
					if handle != 17 || relative != 2 || windows.GetCurrentThreadId() != thread {
						t.Fatal("incorrect priority handle/thread/value")
					}
					return test.priority
				},
				revert: func(handle uintptr) bool {
					reverted++
					if handle != 17 || windows.GetCurrentThreadId() != thread {
						t.Fatal("registration not restored on its original thread")
					}
					return true
				},
			}
			leave, status := activateParallelPriority(api)
			if status != test.want {
				t.Fatalf("priority status=%q", status)
			}
			if reverted != 0 {
				t.Fatal("priority reverted before teardown")
			}
			leave()
			wantFollowups := 0
			if test.register {
				wantFollowups = 1
			}
			if registered != 1 || prioritized != wantFollowups || reverted != wantFollowups {
				t.Fatalf("calls register=%d priority=%d revert=%d", registered, prioritized, reverted)
			}
		})
	}
	leave, _ := activateParallelPriority(parallelPriorityAPI{
		register: func(string) uintptr { return 17 },
		priority: func(uintptr, int32) bool { return true },
		revert:   func(uintptr) bool { return false },
	})
	if leave() {
		t.Fatal("failed restoration incorrectly allowed OS thread reuse")
	}
}

func TestParallelPriorityWindowsActualRegistration(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread := windows.GetCurrentThreadId()
	handle := systemParallelPriority.register("Pro Audio")
	if handle == 0 {
		t.Skip("MMCSS Pro Audio unavailable; fallback retains normal OS scheduling")
	}
	defer func() {
		if !systemParallelPriority.revert(handle) {
			t.Error("MMCSS registration restoration failed")
		}
	}()
	if !systemParallelPriority.priority(handle, parallelCriticalPriority) {
		t.Error("MMCSS critical relative priority was unavailable")
	}
	if windows.GetCurrentThreadId() != thread {
		t.Fatal("MMCSS lifecycle moved OS threads")
	}
	t.Logf("MMCSS Pro Audio critical registered/restored on thread %d; no audio endpoint opened", thread)
}

func TestParallelPriorityWindowsWorkersReadyAndTeardown(t *testing.T) {
	e := parallelFixture(t, 32, false)
	if err := e.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	defer e.closeParallel()
	if e.ParallelWorkers() < 2 {
		t.Skip("host exposes one scheduler lane")
	}
	if e.ParallelPriority() == "" {
		t.Fatal("worker scheduling status unavailable")
	}
	if !strings.Contains(e.ParallelPriority(), "MMCSS") && !strings.Contains(e.ParallelPriority(), "normal OS") {
		t.Fatal("worker priority status omitted fallback")
	}
	if _, fault := e.parallelPrepareSegment(0, 128); fault != 0 {
		t.Fatalf("prepared workers fault=%d", fault)
	}
	e.closeParallel()
	t.Logf("lanes=%d priority=%q workers restored/joined before close returned", e.ParallelWorkers(), e.ParallelPriority())
}
