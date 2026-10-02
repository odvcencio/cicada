//go:build (browser || browser_soak) && windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var browserUser32 = windows.NewLazySystemDLL("user32.dll")
var browserGDI32 = windows.NewLazySystemDLL("gdi32.dll")

func nativeWindowsChromeWindow(powershell string) (uintptr, error) {
	// Window discovery retains the existing read-only, lane-profile selection.
	// No Add-Type or compiler is invoked on the Windows computer.
	script := `$ErrorActionPreference='Stop'; $laneUserData='` + windowsBrowserProfile + `'; $window=$null; Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like ('*' + $laneUserData + '*') } | ForEach-Object { $p=Get-Process -Id $_.ProcessId -ErrorAction SilentlyContinue; if ($p -and $p.MainWindowHandle -ne 0) { $window=$p.MainWindowHandle } }; if (-not $window) { throw 'lane Chrome window not found' }; [ordered]@{handle=$window.ToInt64()} | ConvertTo-Json -Compress`
	output, err := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("discover lane Windows Chrome window: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var result struct {
		Handle uint64 `json:"handle"`
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Handle == 0 {
		return 0, fmt.Errorf("invalid lane Windows Chrome window %q: %v", strings.TrimSpace(string(output)), err)
	}
	return uintptr(result.Handle), nil
}

func resizeNativeWindowsChromeWindow(powershell string, width, height int) error {
	window, err := nativeWindowsChromeWindow(powershell)
	if err != nil {
		return err
	}
	// Match the existing SetWindowPos position and SWP_SHOWWINDOW flag. A
	// foreground request is best-effort, as in the existing PowerShell route.
	result, _, callErr := browserUser32.NewProc("SetWindowPos").Call(window, 0, 30, 30, uintptr(width), uintptr(height), 0x0040)
	if result == 0 {
		return fmt.Errorf("SetWindowPos: %v", callErr)
	}
	browserUser32.NewProc("SetForegroundWindow").Call(window)
	return nil
}

type nativeWindowsRect struct {
	Left, Top, Right, Bottom int32
}

type nativeWindowsPoint struct {
	X, Y int32
}

type nativeWindowsBitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ColorsUsed, ColorsImportant  uint32
}

type nativeWindowsBitmapInfo struct {
	Header nativeWindowsBitmapInfoHeader
	Colors [1]uint32
}

// Cloud crosscompilation also verifies the exact Win32 structure ABI.
var _ [16 - unsafe.Sizeof(nativeWindowsRect{})]byte
var _ [unsafe.Sizeof(nativeWindowsRect{}) - 16]byte
var _ [8 - unsafe.Sizeof(nativeWindowsPoint{})]byte
var _ [unsafe.Sizeof(nativeWindowsPoint{}) - 8]byte
var _ [40 - unsafe.Sizeof(nativeWindowsBitmapInfoHeader{})]byte
var _ [unsafe.Sizeof(nativeWindowsBitmapInfoHeader{}) - 40]byte

func captureNativeWindowsChromeScreenshot(powershell, path string) error {
	window, err := nativeWindowsChromeWindow(powershell)
	if err != nil {
		return err
	}
	// A common screen DC must be released on the thread that acquired it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	browserUser32.NewProc("SetForegroundWindow").Call(window)
	var rect nativeWindowsRect
	if result, _, callErr := browserUser32.NewProc("GetClientRect").Call(window, uintptr(unsafe.Pointer(&rect))); result == 0 {
		return fmt.Errorf("GetClientRect: %v", callErr)
	}
	var point nativeWindowsPoint
	if result, _, callErr := browserUser32.NewProc("ClientToScreen").Call(window, uintptr(unsafe.Pointer(&point))); result == 0 {
		return fmt.Errorf("ClientToScreen: %v", callErr)
	}
	width, height := int(rect.Right), int(rect.Bottom)
	if width <= 0 || height <= 0 || uint64(width)*uint64(height)*4 > uint64(^uint32(0)) {
		return fmt.Errorf("invalid Windows Chrome client dimensions %dx%d", width, height)
	}
	screen, _, callErr := browserUser32.NewProc("GetDC").Call(0)
	if screen == 0 {
		return fmt.Errorf("GetDC: %v", callErr)
	}
	defer browserUser32.NewProc("ReleaseDC").Call(0, screen)
	memory, _, callErr := browserGDI32.NewProc("CreateCompatibleDC").Call(screen)
	if memory == 0 {
		return fmt.Errorf("CreateCompatibleDC: %v", callErr)
	}
	defer browserGDI32.NewProc("DeleteDC").Call(memory)
	bitmap, _, callErr := browserGDI32.NewProc("CreateCompatibleBitmap").Call(screen, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return fmt.Errorf("CreateCompatibleBitmap: %v", callErr)
	}
	defer browserGDI32.NewProc("DeleteObject").Call(bitmap)
	selectObject := browserGDI32.NewProc("SelectObject")
	previous, _, callErr := selectObject.Call(memory, bitmap)
	if previous == 0 || previous == ^uintptr(0) {
		return fmt.Errorf("SelectObject bitmap: %v", callErr)
	}
	selected := true
	defer func() {
		if selected {
			selectObject.Call(memory, previous)
		}
	}()
	// SRCCOPY from the visible screen at the client origin matches the previous
	// System.Drawing CopyFromScreen operation, excluding titlebar and borders.
	if result, _, callErr := browserGDI32.NewProc("BitBlt").Call(memory, 0, 0, uintptr(width), uintptr(height), screen, uintptr(point.X), uintptr(point.Y), 0x00cc0020); result == 0 {
		return fmt.Errorf("BitBlt client screenshot: %v", callErr)
	}
	// GetDIBits requires the bitmap to be deselected. A negative BI_RGB height
	// returns top-down 32-bit BGR pixels, with no extra scanline padding.
	// https://learn.microsoft.com/en-us/windows/win32/api/wingdi/nf-wingdi-getdibits
	if result, _, callErr := selectObject.Call(memory, previous); result == 0 || result == ^uintptr(0) {
		return fmt.Errorf("deselect screenshot bitmap: %v", callErr)
	}
	selected = false
	pixels := make([]byte, width*height*4)
	info := nativeWindowsBitmapInfo{Header: nativeWindowsBitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(nativeWindowsBitmapInfoHeader{})), Width: int32(width), Height: -int32(height),
		Planes: 1, BitCount: 32, SizeImage: uint32(len(pixels)),
	}}
	lines, _, callErr := browserGDI32.NewProc("GetDIBits").Call(screen, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0)
	if lines != uintptr(height) {
		return fmt.Errorf("GetDIBits returned %d/%d scanlines: %v", lines, height, callErr)
	}
	data, err := windowsScreenshotPNG(width, height, pixels)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
