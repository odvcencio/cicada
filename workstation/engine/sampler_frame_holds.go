package main

// samplerFrameHolds belongs to one authenticated frame generation. Each
// pointer owns one pad, and releases cannot affect a different pointer.
type samplerFrameHolds map[int]int

func (h samplerFrameHolds) press(pointer, pad int) bool {
	if pad < 1 || pad > 16 || len(h) >= 32 {
		return false
	}
	if _, exists := h[pointer]; exists {
		return false
	}
	h[pointer] = pad
	return true
}
func (h samplerFrameHolds) release(pointer, pad int) bool {
	if held, ok := h[pointer]; !ok || held != pad {
		return false
	}
	delete(h, pointer)
	return true
}
