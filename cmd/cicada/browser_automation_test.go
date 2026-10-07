//go:build browser

package main

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestBrowserContinuousAutomationLanes(t *testing.T) {
	oldStudio, oldDebug := browserStudioAddress, browserDebugAddress
	t.Cleanup(func() { browserStudioAddress, browserDebugAddress = oldStudio, oldDebug })
	freeAddress := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		return address
	}
	browserStudioAddress, browserDebugAddress = freeAddress(), freeAddress()
	source, err := os.ReadFile("../../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	server := startBrowserStudio(t, source, nil)
	chrome := startBrowserChrome(t, server)
	chrome.setViewport(1440, 1000)
	chrome.navigate("http://" + browserStudioAddress + "/")
	chrome.click("[data-panel-tab=song]")
	chrome.waitFor(`document.querySelectorAll('#song .automation-curve').length === 2 && !document.querySelector('#song').hidden`, 10*time.Second)
	for _, viewport := range []struct {
		width, height int
		file          string
	}{{1440, 1000, "continuous-automation-1440.png"}, {390, 1000, "continuous-automation-390.png"}} {
		chrome.setViewport(viewport.width, viewport.height)
		if string(chrome.eval(`document.documentElement.scrollWidth <= innerWidth`)) != "true" {
			t.Fatal("automation page overflows viewport")
		}
		if string(chrome.eval(`Array.from(document.querySelectorAll('.automation-curve')).every(p=>p.getAttribute('d').startsWith('M ') && !/NaN|Inf/.test(p.getAttribute('d')))`)) != "true" {
			t.Fatal("invalid automation drawing")
		}
		chrome.screenshot(viewport.file)
	}
}
