//go:build browser || browser_soak

package main

import (
	"bytes"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

// These tests exercise path selection only. They never launch Chrome,
// PowerShell, or another host process, and are not browser acceptance.
func TestBrowserPortabilityChromePaths(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want []string
	}{
		{
			name: "Windows standard installations", goos: "windows",
			want: []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`},
		},
		{
			name: "Windows configured installation roots", goos: "windows",
			env:  map[string]string{"ProgramFiles": `D:\Apps\`, "ProgramFiles(x86)": `E:\Apps32/`, "LOCALAPPDATA": `C:\Users\Lane User\AppData\Local\`},
			want: []string{`D:\Apps\Google\Chrome\Application\chrome.exe`, `E:\Apps32\Google\Chrome\Application\chrome.exe`, `C:\Users\Lane User\AppData\Local\Google\Chrome\Application\chrome.exe`},
		},
		{
			name: "Windows explicit Chrome path", goos: "windows",
			env:  map[string]string{"CHROME_BIN": `D:\Chrome for Testing\chrome.exe`},
			want: []string{`D:\Chrome for Testing\chrome.exe`},
		},
		{
			name: "WSL existing installation paths", goos: "linux",
			want: []string{"/mnt/c/Program Files/Google/Chrome/Application/chrome.exe", "/mnt/c/Program Files (x86)/Google/Chrome/Application/chrome.exe"},
		},
		{
			name: "WSL explicit interop path", goos: "linux",
			env:  map[string]string{"CHROME_BIN": "/mnt/d/Chrome for Testing/chrome.exe"},
			want: []string{"/mnt/d/Chrome for Testing/chrome.exe"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string { return test.env[key] }
			if got := windowsChromePaths(test.goos, getenv); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Chrome paths = %q; want %q", got, test.want)
			}
		})
	}
}

func TestBrowserPortabilityPowerShellPath(t *testing.T) {
	cases := []struct {
		name string
		goos string
		root string
		want string
	}{
		{"Windows default", "windows", "", `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{"Windows configured root", "windows", `D:\Windows\`, `D:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{"WSL unchanged", "linux", `D:\Windows`, "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "SystemRoot" {
					return test.root
				}
				return ""
			}
			if got := windowsPowerShellHostPath(test.goos, getenv); got != test.want {
				t.Fatalf("PowerShell path = %q; want %q", got, test.want)
			}
		})
	}
}

func TestBrowserPortabilityProfilePath(t *testing.T) {
	if got := windowsBrowserProfileHostPath("windows"); got != windowsBrowserProfile || strings.Contains(got, "/mnt/") {
		t.Fatalf("native Windows profile path = %q; want %q", got, windowsBrowserProfile)
	}
	if got := windowsBrowserProfileHostPath("linux"); got != windowsBrowserProfileWSL {
		t.Fatalf("WSL profile path = %q; want %q", got, windowsBrowserProfileWSL)
	}
}

func TestBrowserPortabilityScreenshotPNG(t *testing.T) {
	// Odd width, two distinct rows, unused alpha bytes: validate BGR ordering,
	// top-down orientation, exact client-area dimensions, and opaque PNG output.
	pixels := []byte{
		0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 0,
		255, 255, 255, 0, 0, 0, 0, 0, 30, 20, 10, 99,
	}
	data, err := windowsScreenshotPNG(3, 2, pixels)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("PNG dimensions = %v; want 3x2", img.Bounds())
	}
	want := []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}, {0, 0, 0, 255}, {10, 20, 30, 255}}
	for i, expected := range want {
		if got := color.NRGBAModel.Convert(img.At(i%3, i/3)); got != expected {
			t.Fatalf("PNG pixel %d = %v; want %v", i, got, expected)
		}
	}
	for _, invalid := range []struct {
		width, height int
		pixels        []byte
	}{
		{0, 2, nil}, {3, -1, nil}, {3, 2, pixels[:23]}, {int(^uint(0) >> 1), 4, nil},
	} {
		if _, err := windowsScreenshotPNG(invalid.width, invalid.height, invalid.pixels); err == nil {
			t.Fatalf("accepted invalid screenshot dimensions/buffer: %+v", invalid)
		}
	}
}
