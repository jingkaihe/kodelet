import test from 'node:test';
import assert from 'node:assert/strict';

import { canOpenExternalURL } from '../lib/navigation';

test('canOpenExternalURL allows web links', () => {
  assert.equal(canOpenExternalURL('https://kodelet.example.com/docs'), true);
  assert.equal(canOpenExternalURL('http://localhost:8080'), true);
});

test('canOpenExternalURL rejects local files and custom protocols', () => {
  assert.equal(canOpenExternalURL('file:///Users/example/.ssh/config'), false);
  assert.equal(canOpenExternalURL('mailto:support@example.com'), false);
  assert.equal(canOpenExternalURL('vscode://file/tmp/project'), false);
  assert.equal(canOpenExternalURL('javascript:alert(1)'), false);
  assert.equal(canOpenExternalURL('not a url'), false);
});
