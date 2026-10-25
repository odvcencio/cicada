package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeExportSmokeDestinationIsConfinedToReport(t *testing.T) {
	root := t.TempDir()
	report := filepath.Join(root, "run", "startup.json")
	want := filepath.Join(root, "run", "downloads")
	if got, err := nativeSmokeDownloadDir(report, want); err != nil || got != want {
		t.Fatal("confined test destination rejected", got, err)
	}
	for _, path := range []string{root, filepath.Dir(report), filepath.Join(root, "run", "..", "outside")} {
		if _, err := nativeSmokeDownloadDir(report, path); err == nil {
			t.Fatal("accepted a destination outside the test run", path)
		}
	}
	if _, err := nativeSmokeDownloadDir("", want); err == nil {
		t.Fatal("enabled picker bypass in an ordinary launch")
	}
}

func TestNativeExportBootstrapClicksAndCompletion(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed for native export browser-contract checks")
	}
	command := exec.Command(node, "-e", nativeExportBootstrapContract, nativeDownloadBootstrap)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native export browser contract: %v\n%s", err, output)
	}
}

const nativeExportBootstrapContract = `
const assert = require('node:assert/strict'), vm = require('node:vm');
const script = process.argv[1], calls = [], subscriptions = new Map();
let listener, listeners = 0, reject = false;
const window = {gosxDesktop: {
  on(name, fn) { subscriptions.set(name, fn); },
  call(name, value) { calls.push({name, value}); return reject ? Promise.reject(new Error('disk full')) : Promise.resolve({accepted: true}); }
}};
window.top = window;
const document = {
  addEventListener(name, fn, capture) { assert.equal(name, 'click'); assert.equal(capture, true); listener = fn; listeners++; },
  createElement() { return {textContent: '', setAttribute() {}}; }
};
const context = vm.createContext({window, document, location: new URL('http://127.0.0.1:8181/studio'), URL, Promise, Date,
  CustomEvent: class { constructor(type, props) { this.type = type; Object.assign(this, props); } }
});
vm.runInContext(script, context); vm.runInContext(script, context);
assert.equal(listeners, 1);
function link(href) {
  const attrs = new Map(), parent = {feedback: null, querySelector() { return this.feedback; }, appendChild(node) { this.feedback = node; }};
  return {href, parentElement: parent, events: [], closest() { return this; },
    getAttribute(name) { return attrs.get(name) ?? null; }, setAttribute(name, value) { attrs.set(name, value); }, removeAttribute(name) { attrs.delete(name); },
    dispatchEvent(event) { this.events.push(event); }};
}
function click(target, props = {}) {
  const event = {target, button: 0, defaultPrevented: false, prevented: false, stopped: false,
    preventDefault() { this.prevented = true; }, stopImmediatePropagation() { this.stopped = true; }, ...props};
  listener(event); return event;
}
const complete = value => subscriptions.get('cicada.export.saved')(value);
(async () => {
  for (const invalid of ['https://example.test/media/stem-exports/abc.zip', '/media/stem-exports/abc.zip?path=secret', '/media/stem-exports/abc.zip#hash',
    '/media/other/file.zip', '/media/stem-exports/../secret.zip', '/media/stem-exports/abc%2fdef.zip']) {
    assert.equal(click(link(invalid)).prevented, false, invalid);
  }
  assert.equal(calls.length, 0);
  for (const [path, name] of [['/media/stem-exports/abc.zip', 'song-stems.zip'], ['/media/exports/abc.wav', 'song.wav'], ['/media/midi-exports/abc.mid', 'song.mid']]) {
    const target = link(path), event = click(target), request = calls.at(-1);
    assert.equal(event.prevented, true); assert.equal(event.stopped, true);
    assert.equal(request.name, 'cicada.export.save'); assert.equal(request.value.path, path);
    assert.deepEqual(Object.keys(request.value).sort(), ['path', 'requestId']);
    assert.equal(target.getAttribute('aria-disabled'), 'true');
    const count = calls.length; click(target); assert.equal(calls.length, count);
    complete({requestId: 'wrong', state: 'saved', name}); assert.equal(target.getAttribute('aria-disabled'), 'true');
    complete({requestId: request.value.requestId, state: 'saved', name});
    assert.equal(target.getAttribute('aria-disabled'), null); assert.equal(target.getAttribute('aria-busy'), null);
    assert.equal(target.parentElement.feedback.textContent, 'Saved ' + name + '.');
    assert.equal(target.events[0].type, 'cicada:export-saved');
  }
  const cancel = link('/media/stem-exports/cancel.zip'); click(cancel);
  complete({requestId: calls.at(-1).value.requestId, state: 'cancelled'});
  assert.match(cancel.parentElement.feedback.textContent, /cancelled.*still available/);
  assert.equal(cancel.getAttribute('aria-disabled'), null);
  reject = true;
  const fail = link('/media/stem-exports/fail.zip'); click(fail);
  await new Promise(resolve => setImmediate(resolve));
  assert.match(fail.parentElement.feedback.textContent, /Could not save.*disk full/);
  assert.equal(fail.getAttribute('aria-disabled'), null);
  window.gosxDesktop = null;
  assert.equal(click(link('/media/stem-exports/browser.zip')).prevented, false);
})().catch(error => { console.error(error); process.exitCode = 1; });
`
