import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { buildSidecarArgs, resolveSidecarBinary } from '../lib/sidecar';

test('buildSidecarArgs creates serve invocation for workspace', () => {
  assert.deepEqual(
    buildSidecarArgs({
      host: '127.0.0.1',
      port: 43123,
      workspace: '/tmp/project',
    }),
    ['serve', '--host', '127.0.0.1', '--port', '43123', '--cwd', '/tmp/project'],
  );
});

test('resolveSidecarBinary returns override path when --kodelet-path is set', () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'kodelet-desktop-sidecar-'));
  const binaryPath = path.join(tempDir, 'kodelet-custom');
  fs.writeFileSync(binaryPath, '');

  const resolved = resolveSidecarBinary({
    argv: ['electron', '.', '--kodelet-path', binaryPath],
    isPackaged: false,
    projectRoot: tempDir,
    resourcesPath: '',
  });

  assert.equal(resolved, binaryPath);
});

test('resolveSidecarBinary defaults to kodelet on PATH in development', () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'kodelet-desktop-sidecar-'));

  const resolved = resolveSidecarBinary({
    argv: ['electron', '.'],
    isPackaged: false,
    projectRoot: tempDir,
    resourcesPath: '',
  });

  assert.equal(resolved, 'kodelet');
});
