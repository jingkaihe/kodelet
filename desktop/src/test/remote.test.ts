import test from 'node:test';
import assert from 'node:assert/strict';

import { normalizeRemoteServerURL } from '../lib/remote';

test('normalizeRemoteServerURL preserves explicit https origin', () => {
  assert.equal(normalizeRemoteServerURL('https://kodelet.example.com/'), 'https://kodelet.example.com');
});

test('normalizeRemoteServerURL defaults localhost without scheme to http', () => {
  assert.equal(normalizeRemoteServerURL('localhost:8080'), 'http://localhost:8080');
});

test('normalizeRemoteServerURL rejects path-prefixed servers', () => {
  assert.throws(
    () => normalizeRemoteServerURL('https://kodelet.example.com/app'),
    /root of a kodelet serve instance/,
  );
});

