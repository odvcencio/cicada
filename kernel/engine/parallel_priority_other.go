//go:build !tinygo && !js && !windows

package engine

// Native workers remain under the operating system's normal scheduling policy.
// Linux realtime privileges are not assumed or changed by the sound engine.
func enterParallelPriority() (func() bool, string) {
	return leaveParallelPriority, "normal OS scheduling"
}
func leaveParallelPriority() bool { return true }
