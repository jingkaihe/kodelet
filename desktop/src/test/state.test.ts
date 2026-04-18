import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  getStateFilePath,
  loadDesktopState,
  resolveInitialWorkspace,
  saveDesktopState,
  shouldConnectToRemote,
} from '../lib/state';

test('loadDesktopState returns empty local state when file is missing', () => {
  const userDataPath = fs.mkdtempSync(path.join(os.tmpdir(), 'kodelet-desktop-state-'));
  assert.deepEqual(loadDesktopState(userDataPath), {
    connectionMode: 'local',
    workspacePath: '',
    remoteUrl: '',
  });
});

test('saveDesktopState persists connection details', () => {
  const userDataPath = fs.mkdtempSync(path.join(os.tmpdir(), 'kodelet-desktop-state-'));
  saveDesktopState(userDataPath, {
    connectionMode: 'remote',
    workspacePath: '  /tmp/workspace  ',
    remoteUrl: '  https://kodelet.example.com  ',
  });

  assert.deepEqual(loadDesktopState(userDataPath), {
    connectionMode: 'remote',
    workspacePath: '/tmp/workspace',
    remoteUrl: 'https://kodelet.example.com',
  });
  assert.ok(fs.existsSync(getStateFilePath(userDataPath)));
});

test('resolveInitialWorkspace falls back to home directory for missing paths', () => {
  assert.equal(resolveInitialWorkspace({ workspacePath: '/definitely/missing' }), os.homedir());
});

test('shouldConnectToRemote requires remote mode and URL', () => {
  assert.equal(shouldConnectToRemote({ connectionMode: 'remote', remoteUrl: 'https://kodelet.example.com' }), true);
  assert.equal(shouldConnectToRemote({ connectionMode: 'remote', remoteUrl: '' }), false);
  assert.equal(shouldConnectToRemote({ connectionMode: 'local', remoteUrl: 'https://kodelet.example.com' }), false);
});

