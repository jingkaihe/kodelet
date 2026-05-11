import test from 'node:test';
import assert from 'node:assert/strict';

import { buildRemoteServerURL, normalizeRemoteServerURL, removeRemoteServerToken } from '../lib/remote';

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

test('normalizeRemoteServerURL preserves auth token query', () => {
  assert.equal(
    normalizeRemoteServerURL('https://kodelet.example.com/?token=secret&unused=value'),
    'https://kodelet.example.com?token=secret',
  );
});

test('buildRemoteServerURL forwards auth token to API endpoint', () => {
  assert.equal(
    buildRemoteServerURL('https://kodelet.example.com?token=secret', '/api/chat/settings'),
    'https://kodelet.example.com/api/chat/settings?token=secret',
  );
});

test('removeRemoteServerToken strips auth token before persistence', () => {
  assert.equal(
    removeRemoteServerToken('https://kodelet.example.com?token=secret'),
    'https://kodelet.example.com',
  );
});
