'use strict';
const assert = require('node:assert/strict');

// Live closes its lease with a form-encoded Beacon during pagehide. It is a
// lifecycle release, separate from JSON score edits; no other form action is
// admitted here. Keep tokens and lease IDs out of assertion diagnostics.
function liveReleaseBeacon(request, fixtureCSRFTokens) {
  if (request.method() !== 'POST' || new URL(request.url()).pathname !== '/__actions/live' ||
      !/^application\/x-www-form-urlencoded(?:;|$)/i.test(request.headers()['content-type'] || '')) return null;
  const payload = new URLSearchParams(request.postData() || '');
  assert.deepEqual([...payload.keys()].sort(), ['csrf_token', 'lease', 'sequence', 'type'], 'Live pagehide Beacon has only release fields');
  assert.equal(payload.get('type'), 'release', 'form-encoded Live action must release its lease');
  assert.ok(payload.get('csrf_token') && fixtureCSRFTokens.has(payload.get('csrf_token')), 'Live release must carry the fixture CSRF token');
  assert.ok(/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(payload.get('lease')), 'Live release must identify a UUID lease');
  assert.ok(/^[1-9]\d*$/.test(payload.get('sequence')) && Number.isSafeInteger(Number(payload.get('sequence'))), 'Live release sequence must be a positive safe integer');
  return {lease: payload.get('lease'), sequence: Number(payload.get('sequence'))};
}
module.exports = {liveReleaseBeacon};
