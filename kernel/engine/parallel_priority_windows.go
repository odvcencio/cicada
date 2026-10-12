//go:build !tinygo && !js && windows

package engine

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const parallelCriticalPriority int32 = 2

type parallelPriorityAPI struct {
	register func(task string) uintptr
	priority func(handle uintptr, relative int32) bool
	revert   func(handle uintptr) bool
}

var parallelAVRT = windows.NewLazySystemDLL("avrt.dll")
var parallelRegisterMMCSS = parallelAVRT.NewProc("AvSetMmThreadCharacteristicsW")
var parallelSetMMCSSPriority = parallelAVRT.NewProc("AvSetMmThreadPriority")
var parallelRevertMMCSS = parallelAVRT.NewProc("AvRevertMmThreadCharacteristics")

var systemParallelPriority = parallelPriorityAPI{
	register: func(task string) uintptr {
		// Resolve every entry point before acquiring a handle so cleanup can
		// always restore this locked thread, including priority-call failures.
		if parallelRegisterMMCSS.Find() != nil || parallelSetMMCSSPriority.Find() != nil || parallelRevertMMCSS.Find() != nil {
			return 0
		}
		name, err := windows.UTF16PtrFromString(task)
		if err != nil {
			return 0
		}
		var index uint32 // Required to be zero for the initial registration.
		handle, _, _ := parallelRegisterMMCSS.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&index)))
		runtime.KeepAlive(name)
		runtime.KeepAlive(&index)
		return handle
	},
	priority: func(handle uintptr, relative int32) bool {
		ok, _, _ := parallelSetMMCSSPriority.Call(handle, uintptr(relative))
		return ok != 0
	},
	revert: func(handle uintptr) bool {
		ok, _, _ := parallelRevertMMCSS.Call(handle)
		return ok != 0
	},
}

// Registration and restoration run once on the same locked OS thread, before
// worker readiness and before teardown publication. No endpoint is opened.
// https://learn.microsoft.com/en-us/windows/win32/api/avrt/nf-avrt-avsetmmthreadcharacteristicsw
// https://learn.microsoft.com/en-us/windows/win32/api/avrt/nf-avrt-avrevertmmthreadcharacteristics
func enterParallelPriority() (func() bool, string) {
	return activateParallelPriority(systemParallelPriority)
}

func activateParallelPriority(api parallelPriorityAPI) (func() bool, string) {
	handle := api.register("Pro Audio")
	if handle == 0 {
		return leaveParallelPriority, "normal OS scheduling; MMCSS unavailable"
	}
	status := "MMCSS Pro Audio"
	if api.priority(handle, parallelCriticalPriority) {
		status = "MMCSS Pro Audio critical"
	}
	return func() bool { return api.revert(handle) }, status
}
func leaveParallelPriority() bool { return true }
