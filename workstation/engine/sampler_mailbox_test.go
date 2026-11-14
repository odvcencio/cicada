package main

import "testing"

func TestSamplerMailboxBoundsResultsAndKeepsNewest(t *testing.T) {
	var box samplerMailbox[int]
	epoch, notify := box.put(1)
	if !notify {
		t.Fatal("missing first notifier")
	}
	for i := 2; i <= 10000; i++ {
		if _, notify := box.put(i); notify {
			t.Fatal("duplicate notifier")
		}
	}
	if value, ok := box.take(); !ok || value != 10000 {
		t.Fatalf("got %d, %t", value, ok)
	}
	if box.current(epoch) {
		t.Fatal("consumed notifier remained current")
	}
	if _, notify := box.put(7); !notify {
		t.Fatal("next notification not armed")
	}
	box.clear()
	if _, ok := box.take(); ok {
		t.Fatal("clear retained result")
	}
}

func TestSamplerAcceptedResampleReceiptSurvivesOverflow(t *testing.T) {
	entered, unblock, handled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var box samplerMailbox[string]
	var x *samplerExecutor
	postCount := 1 // The server has already accepted exactly this submitted fingerprint.
	var acknowledged string
	x = newSamplerExecutor(1, func(c samplerCommand, _ func() bool) {
		if c.kind == "block" {
			close(entered)
			<-unblock
			return
		}
		if c.kind == "receipt" {
			acknowledged, _ = box.take()
			close(handled)
		}
	}, func() bool { return true })
	x.ready = func() {
		if _, ok := box.renotify(); ok {
			x.submit(samplerCommand{kind: "receipt"})
		}
	}
	x.submit(samplerCommand{kind: "block"})
	samplerWait(t, entered)
	if _, notify := box.put("submitted-performance"); !notify {
		t.Fatal("receipt not armed")
	}
	if !x.submit(samplerCommand{kind: "receipt"}) {
		t.Fatal("receipt notification rejected")
	}
	if x.submit(samplerCommand{kind: "overflow"}) {
		t.Fatal("overflow accepted")
	}
	close(unblock)
	samplerWait(t, handled)
	if acknowledged != "submitted-performance" || postCount != 1 {
		t.Fatalf("receipt=%q posts=%d", acknowledged, postCount)
	}
	x.close()
}
