//go:build windows

package main

import "m31labs.dev/gosx/desktop"

func platformMenu(_ *host, menu desktop.Menu) desktop.Menu { return menu }
