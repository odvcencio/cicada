//go:build linux && (amd64 || arm64)

package rt

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const schedOther = 0

// RaiseProcessThreads raises SCHED_OTHER threads to SCHED_FIFO priority p.
// Threads already running with a real-time policy are left unchanged.
func RaiseProcessThreads(p int) (restore func(), raised int, err error) {
	if p < 1 || p > 99 {
		return func() {}, 0, fmt.Errorf("rt: invalid SCHED_FIFO priority %d", p)
	}
	previous := make(map[int]unix.SchedAttr)
	existing := make(map[int]bool)
	var firstErr error
	for pass := 0; pass < 8; pass++ {
		tids, listErr := processThreads()
		if listErr != nil {
			firstErr = listErr
			break
		}
		changed := false
		for _, tid := range tids {
			existing[tid] = true
			if _, done := previous[tid]; done {
				continue
			}
			attr, getErr := unix.SchedGetAttr(tid, 0)
			if getErr != nil || attr.Policy != schedOther {
				continue
			}
			next := *attr
			next.Policy = unix.SCHED_FIFO
			next.Priority = uint32(p)
			if setErr := unix.SchedSetAttr(tid, &next, 0); setErr != nil {
				if firstErr == nil {
					firstErr = setErr
				}
				continue
			}
			previous[tid] = *attr
			changed = true
		}
		if !changed {
			break
		}
	}
	restore = func() {
		tids, listErr := processThreads()
		if listErr != nil {
			return
		}
		for _, tid := range tids {
			back, wasRaised := previous[tid]
			if !wasRaised && existing[tid] {
				continue
			}
			attr, getErr := unix.SchedGetAttr(tid, 0)
			if getErr != nil || attr.Policy != unix.SCHED_FIFO || attr.Priority != uint32(p) || attr.Flags&unix.SCHED_FLAG_RESET_ON_FORK != 0 {
				continue
			}
			if !wasRaised {
				back = unix.SchedAttr{Size: unix.SizeofSchedAttr}
			}
			_ = unix.SchedSetAttr(tid, &back, 0)
		}
	}
	return restore, len(previous), firstErr
}

func processThreads() ([]int, error) {
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}
	tids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if tid, err := strconv.Atoi(entry.Name()); err == nil {
			tids = append(tids, tid)
		}
	}
	return tids, nil
}
