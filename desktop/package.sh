#!/usr/bin/env bash
# Build the Cicada Studio desktop app for Windows x64 and package it with
# `gosx desktop package` (Setup.exe, portable ZIP, signed latest.json).
#
# Usage:
#   desktop/package.sh --version 0.1.0 --public-key <base64 Ed25519 public key> \
#       --manifest-key <Ed25519 private key file> [--output dist/desktop] [--publisher NAME] \
#       [--gosx PATH]
#
# --gosx selects a prebuilt gosx CLI; by default the script installs the gosx
# version that desktop/go.mod requires into a temporary folder.
#
# Needs: go, curl, unzip, sha256sum. Runs from any directory.
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
while [[ $# -gt 0 ]]; do
	case "$1" in
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
if [[ -z "$version" || -z "$public_key" || -z "$manifest_key" ]]; then
	echo "usage: desktop/package.sh --version X.Y.Z --public-key KEY --manifest-key FILE [--output DIR] [--publisher NAME]" >&2
	exit 2
fi

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
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

export GOWORK=off
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
