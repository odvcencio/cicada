#!/usr/bin/env python3
"""Exercise the real macOS host against a deterministic HTTP fixture.

This checks WKWebView, the production bridge and native file saving, not the
Studio renderer or audio engine. Full Studio workflows run in the other lane.
"""

import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
from pathlib import Path
import platform
import plistlib
import shutil
from socketserver import TCPServer
import sys
import tempfile
import time
import zipfile


EXPORT_PATH = "/media/stem-exports/host-fixture.zip"
SCORE = 'title "Native host fixture"\ntempo 120\n'
PAGE = b'''<!doctype html><html lang="en"><meta charset="utf-8">
<title>Cicada native host fixture</title>
<body data-native-host-fixture="1"><h1>Native host fixture</h1>
<p>This fixture tests the desktop bridge, not the Studio renderer.</p>
<div><a id="host-download" href="/media/stem-exports/host-fixture.zip"
download="host-fixture.zip">Save fixture ZIP</a></div></body></html>'''


class FixtureHTTPServer(ThreadingHTTPServer):
    def server_bind(self):
        # HTTPServer resolves the bound address with getfqdn. This private
        # loopback fixture needs no DNS, including on cold macOS CI runners.
        TCPServer.server_bind(self)
        self.server_name = "localhost"
        self.server_port = self.server_address[1]


def fixture_bytes():
    data = io.BytesIO()
    with zipfile.ZipFile(data, "w", compression=zipfile.ZIP_STORED) as archive:
        entry = zipfile.ZipInfo("fixture.txt", date_time=(1980, 1, 1, 0, 0, 0))
        archive.writestr(entry, b"Cicada native host save fixture.\n" * 64)
    return data.getvalue()


def fixture_handler():
    archive = fixture_bytes()
    metadata = json.dumps({"scope": "native-host-fixture", "bytes": len(archive),
                           "sha256": hashlib.sha256(archive).hexdigest()}).encode()

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path in {"/", "/studio"}:
                body, media_type = PAGE, "text/html; charset=utf-8"
            elif self.path == "/fixture.json":
                body, media_type = metadata, "application/json"
            elif self.path == EXPORT_PATH:
                body, media_type = archive, "application/zip"
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", media_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            if self.path == EXPORT_PATH:
                self.send_header("Content-Disposition", 'attachment; filename="host-fixture.zip"')
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    return Handler


def fixture_main(argv):
    parser = argparse.ArgumentParser(description="Private native-host HTTP fixture")
    parser.add_argument("score", type=Path)
    parser.add_argument("--listen", required=True)
    parser.add_argument("--audio", required=True)
    args = parser.parse_args(argv)
    if args.listen != "127.0.0.1:0" or args.audio != "null" or not args.score.is_file():
        parser.error("fixture requires an existing score, loopback ephemeral port and null audio")
    # The fixture never opens an audio device or writes the supplied score.
    with FixtureHTTPServer(("127.0.0.1", 0), fixture_handler()) as server:
        print(f"Cicada Studio: http://127.0.0.1:{server.server_port}/", flush=True)
        server.serve_forever()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("native host validation requires macOS")
    host = args.host.resolve()
    if not host.is_file():
        parser.error(f"compiled native host is missing: {host}")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    from smoke import check_launch, isolated_app

    workflow = Path(__file__).resolve().parent / "testdata/native-host-workflow.js"
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="cicada-host-fixture-") as temporary:
        root = Path(temporary)
        app = root / "Cicada Native Host.app"
        binaries = app / "Contents/MacOS"
        binaries.mkdir(parents=True)
        shutil.copy2(host, binaries / "cicada-studio")
        # This file's studio subcommand is self-contained and uses only stdlib.
        # No checkout or user project is mounted in the fixture bundle.
        shutil.copy2(Path(__file__), binaries / "cicada")
        (binaries / "cicada").chmod(0o700)
        (app / "Contents/Info.plist").write_bytes(plistlib.dumps({
            "CFBundleExecutable": "cicada-studio", "CFBundlePackageType": "APPL",
            "CFBundleIdentifier": "dev.cicada.native-host-fixture",
            "CFBundleName": "Cicada Native Host Fixture", "CFBundleVersion": "1",
            "NSHighResolutionCapable": True, "NSPrincipalClass": "NSApplication",
            "LSMinimumSystemVersion": "12.0",
        }))
        score = root / "main.cicada"
        score.write_text(SCORE)
        with isolated_app(app, output) as binary:
            result = check_launch(binary, output, "normal", workflow, timeout=45,
                                  score=score, download_test=True)
            if score.read_text() != SCORE:
                raise RuntimeError("native host smoke changed its fixture score")
        receipt = result["startup"]["nativeExportSave"]
        expected = fixture_bytes()
        if receipt["bytes"] != len(expected) or receipt["sha256"] != hashlib.sha256(expected).hexdigest():
            raise RuntimeError("saved file differs from the deterministic host fixture")
        result.update({
            "scope": "production native host with deterministic fixture HTTP sidecar; not Studio rendering",
            "machine": platform.machine(), "sourceUnchanged": True,
            "hostSHA256": hashlib.sha256(host.read_bytes()).hexdigest(),
            "physicalAudioVerified": False, "nativePickerAutomated": False,
            "elapsedSeconds": time.monotonic() - started,
        })
        (output / "report.json").write_text(json.dumps(result, indent=2) + "\n")
    print("native host WKWebView, bridge, download and shutdown: passed", flush=True)


if __name__ == "__main__":
    if sys.argv[1:2] == ["studio"]:
        fixture_main(sys.argv[2:])
    else:
        main()
