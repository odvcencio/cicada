#!/usr/bin/env bash
# Build and package the Cicada Studio desktop app.
#
# --target picks the platform. The default follows the machine: darwin on macOS,
# windows anywhere else.
#
# Windows x64: builds the executables and packages them with
# `gosx desktop package` (Setup.exe, portable ZIP, signed latest.json).
#
#   desktop/package.sh [--target windows] --version 0.1.0 \
#       --public-key <base64 Ed25519 public key> \
#       --manifest-key <Ed25519 private key file> [--output dist/desktop] [--publisher NAME] \
#       [--gosx PATH]
#
#   --gosx selects a prebuilt gosx CLI; by default the script installs the gosx
#   version that desktop/go.mod requires into a temporary folder.
#
#   Needs: go, curl, unzip, sha256sum.
#
# macOS: assembles "Cicada Studio.app" with the desktop host, the cicada CLI
# sidecar, cicada-workstation, and the GoSX runtime (build.json, assets,
# public), and signs it ad hoc (development builds; no Developer ID or
# notarization). The host finds siblings in Contents/MacOS/ and the GoSX
# runtime at Contents/MacOS/workstation/, so the bundle is self-contained.
#
#   desktop/package.sh [--target darwin] [--version 0.1.0] [--output dist/macos]
#
#   All builds use pinned modules with GOWORK=off, including the macOS host.
#   Packaging checks backend availability before building the bundle.
#
#   Needs: go, tinygo (CI uses 0.41.1), wasm-opt (Binaryen), codesign, plutil.
#   The last two ship with macOS. WASMOPT can select a wasm-opt executable.
#
# Runs from any directory.
set -euo pipefail

version=""
public_key=""
manifest_key=""
publisher="Cicada"
download_page="https://github.com/odvcencio/cicada/releases"
notes=""
desktop_shortcut=false
output=""
gosx=""
target=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--target) target="$2"; shift 2 ;;
	--version) version="$2"; shift 2 ;;
	--public-key) public_key="$2"; shift 2 ;;
	--manifest-key) manifest_key="$2"; shift 2 ;;
	--publisher) publisher="$2"; shift 2 ;;
	--download-page) download_page="$2"; shift 2 ;;
	--notes) notes="$2"; shift 2 ;;
	--desktop-shortcut) desktop_shortcut=true; shift ;;
	--output) output="$2"; shift 2 ;;
	--gosx) gosx="$(realpath -- "$2")"; shift 2 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
if [[ -z "$target" ]]; then
	case "$(uname -s)" in
	Darwin) target=darwin ;;
	*) target=windows ;;
	esac
fi
case "$target" in
windows | darwin) ;;
*) echo "unknown --target: $target (use windows or darwin)" >&2; exit 2 ;;
esac
if [[ "$target" == windows && ( -z "$version" || -z "$public_key" || -z "$manifest_key" ) ]]; then
	echo "usage: desktop/package.sh [--target windows] --version X.Y.Z --public-key KEY --manifest-key FILE [--output DIR] [--publisher NAME]" >&2
	echo "       desktop/package.sh --target darwin [--version X.Y.Z] [--output DIR]" >&2
	exit 2
fi

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# A developer's workspace must not replace release dependencies with local code.
export GOWORK=off

# package_darwin assembles an ad-hoc-signed "Cicada Studio.app" around the host
# executable. It uses no GNU-only tools, so it runs on the stock macOS userland.
package_darwin() {
	version="${version:-0.0.0-dev}"
	if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z][0-9A-Za-z.+-]*)?$ ]]; then
		echo "invalid --version: $version (use X.Y.Z with an optional prerelease or build suffix)" >&2
		exit 2
	fi
	local tool
	for tool in go tinygo "${WASMOPT:-wasm-opt}" codesign plutil; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			echo "$tool not found: macOS packaging requires Go, TinyGo, wasm-opt (Binaryen), codesign, and plutil" >&2
			exit 1
		fi
	done

	work="$(mktemp -d)"
	trap 'rm -rf -- "$work"' EXIT
	cat >"$work/check-platform.go" <<'GO'
package main

import (
	"fmt"
	"os"

	"m31labs.dev/gosx/desktop"
)

func main() {
	if err := desktop.Available(); err != nil {
		fmt.Fprintf(os.Stderr, "macOS desktop backend unavailable: %v\nCheck the pinned GoSX dependency in desktop/go.mod.\n", err)
		os.Exit(1)
	}
}
GO
	(cd "$repo/desktop" && GOOS=darwin CGO_ENABLED=0 go run "$work/check-platform.go")

	local out app
	out="${output:-$repo/dist/macos}"
	mkdir -p -- "$out"
	out="$(cd -- "$out" && pwd)"
	app="$work/Cicada Studio.app"
	mkdir -p -- "$app/Contents/MacOS"

	# CFBundleVersion and CFBundleShortVersionString take digits and dots, so a
	# pre-release suffix such as -dev stays in the binary's version only.
	local plist_version="${version%%[-+]*}"
	(cd "$repo/desktop" && GOOS=darwin CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.buildVersion=$version" \
		-o "$app/Contents/MacOS/cicada-studio" .)
	# Build the cicada CLI sidecar and the GoSX workstation. Both land next to
	# the host in Contents/MacOS/ so findCicada and the sidecar can resolve
	# them without PATH or extra flags.
	(cd "$repo" && GOWORK=off GOOS=darwin CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w" \
		-o "$app/Contents/MacOS/cicada" ./cmd/cicada)
	# Build and install the GoSX runtime (build.json, hashed assets, public
	# tree) alongside the workstation binary. runtimeRoot() in
	# workstation/runtime.go checks Contents/MacOS/workstation/build.json
	# first, so this path flips the page to bootstrapMode=full and loads the
	# full GoSX runtime instead of falling back to raw browser form POSTs.
	# Keep the CLI and generated runtime on the workstation's pinned GoSX
	# version independently of the desktop host's dependency. Generate first
	# so the workstation executable includes the same UI as its runtime assets.
	# The production build also compiles the server with trimpath and the
	# configured symbol stripping. Reuse it instead of compiling it twice.
	(cd "$repo/workstation" && GOWORK=off GOOS=darwin CGO_ENABLED=0 go run -mod=mod m31labs.dev/gosx/cmd/gosx build --prod .)
	cp "$repo/workstation/dist/server/app" "$app/Contents/MacOS/cicada-workstation"
	mkdir -p "$app/Contents/MacOS/workstation"
	cp "$repo/workstation/dist/build.json" "$app/Contents/MacOS/workstation/"
	cp -R "$repo/workstation/dist/assets" "$repo/workstation/dist/public" \
		"$app/Contents/MacOS/workstation/"
	cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>Cicada Studio</string>
	<key>CFBundleExecutable</key>
	<string>cicada-studio</string>
	<key>CFBundleIdentifier</key>
	<string>dev.cicada.studio</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>Cicada Studio</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$plist_version</string>
	<key>CFBundleVersion</key>
	<string>$plist_version</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.music</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSUIElement</key>
	<false/>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSMicrophoneUsageDescription</key>
	<string>Cicada Studio uses microphone input to record voices, instruments, and samples into your project.</string>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
	<key>NSMicrophoneUsageDescription</key>
	<string>Cicada records vocals and instruments from your microphone when you start recording.</string>
	<key>CFBundleDocumentTypes</key>
	<array>
		<dict>
			<key>CFBundleTypeName</key>
			<string>Cicada score</string>
			<key>CFBundleTypeRole</key>
			<string>Editor</string>
			<key>LSHandlerRank</key>
			<string>Owner</string>
			<key>LSItemContentTypes</key>
			<array><string>dev.cicada.score</string></array>
		</dict>
	</array>
	<key>UTExportedTypeDeclarations</key>
	<array>
		<dict>
			<key>UTTypeIdentifier</key>
			<string>dev.cicada.score</string>
			<key>UTTypeDescription</key>
			<string>Cicada score</string>
			<key>UTTypeConformsTo</key>
			<array><string>public.plain-text</string></array>
			<key>UTTypeTagSpecification</key>
			<dict>
				<key>public.filename-extension</key>
				<array><string>cicada</string></array>
			</dict>
		</dict>
	</array>
</dict>
</plist>
PLIST
	printf 'APPL????' >"$app/Contents/PkgInfo"
	plutil -lint "$app/Contents/Info.plist" >/dev/null

	codesign -s - --force --timestamp=none --deep "$app"
	codesign --verify --deep --strict "$app"
	# Keep the previous app intact until its replacement is built and verified.
	rm -rf -- "$out/Cicada Studio.app"
	mv -- "$app" "$out/"
	echo "Cicada Studio $version packaged at $out/Cicada Studio.app"
}

if [[ "$target" == darwin ]]; then
	package_darwin
	exit 0
fi

output="$(realpath -m -- "${output:-$repo/dist/desktop}")"
manifest_key="$(realpath -- "$manifest_key")"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
stage="$work/stage"
mkdir -p "$stage/licenses" "$output"

# WebView2 SDK 1.0.4191.47 supplies WebView2Loader.dll (BSD-style license).
sdk_version=1.0.4191.47
sdk_sha256=f492bbf547d0da329553b6727435b677579b1e9f91cc9e4a1ad029366d5f23d0
loader_sha256=c66e4a92fdc7a216118e43b7a5024ea2200e8c43f9310bf20d96a0084f82c5bc
cache="${XDG_CACHE_HOME:-$HOME/.cache}/cicada-desktop"
nupkg="$cache/microsoft.web.webview2.$sdk_version.nupkg"
mkdir -p "$cache"
if [[ ! -s "$nupkg" ]] || ! printf '%s  %s\n' "$sdk_sha256" "$nupkg" | sha256sum --check --status; then
	curl -fsSL --retry 5 -o "$nupkg" "https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/$sdk_version/microsoft.web.webview2.$sdk_version.nupkg"
fi
printf '%s  %s\n' "$sdk_sha256" "$nupkg" | sha256sum --check --status
unzip -q -o "$nupkg" 'runtimes/win-x64/native/WebView2Loader.dll' LICENSE.txt -d "$work/sdk"
printf '%s  %s\n' "$loader_sha256" "$work/sdk/runtimes/win-x64/native/WebView2Loader.dll" | sha256sum --check --status
cp "$work/sdk/runtimes/win-x64/native/WebView2Loader.dll" "$stage/"
cp "$work/sdk/LICENSE.txt" "$stage/licenses/WebView2-SDK-LICENSE.txt"
cp "$repo/LICENSE" "$stage/licenses/Cicada-LICENSE.txt"

# Build the framework runtime on the host before cross-compiling executables.
# Mutable scores, take journals, and settings never enter the GoSX bundle.
(cd "$repo/workstation" && go run -mod=mod m31labs.dev/gosx/cmd/gosx build --prod .)
mkdir -p "$stage/workstation"
cp "$repo/workstation/dist/build.json" "$stage/workstation/"
cp -R "$repo/workstation/dist/assets" "$repo/workstation/dist/public" "$stage/workstation/"

export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
(cd "$repo" && go build -trimpath -ldflags="-s -w" -o "$stage/cicada.exe" ./cmd/cicada)
(cd "$repo/workstation" && go build -trimpath -ldflags="-s -w" -o "$stage/cicada-workstation.exe" .)
(cd "$repo/desktop" && go build -trimpath -ldflags="-H=windowsgui -s -w -X main.buildVersion=$version" -o "$stage/cicada-studio.exe" .)
unset GOOS GOARCH

cat >"$work/config.json" <<JSON
{
  "app_id": "dev.cicada.studio",
  "name": "Cicada Studio",
  "publisher": "$publisher",
  "version": "$version",
  "host_exe": "cicada-studio.exe",
  "update_public_key": "$public_key",
  "data_dir": "%LOCALAPPDATA%\\\\Cicada Studio",
  "download_page": "$download_page",
  "notes": "${notes:-Cicada Studio $version}",
  "desktop_shortcut": $desktop_shortcut
}
JSON
if [[ -z "$gosx" ]]; then
	gosx_version="$(cd "$repo/desktop" && go list -m -f '{{.Version}}' m31labs.dev/gosx)"
	GOBIN="$work/bin" go install "m31labs.dev/gosx/cmd/gosx@$gosx_version"
	gosx="$work/bin/gosx"
fi
GOSX_SKIP_VERSION_CHECK=1 "$gosx" desktop package \
	--input "$stage" --config "$work/config.json" --output "$output" --manifest-key "$manifest_key"
echo "Cicada Studio $version packaged in $output"
