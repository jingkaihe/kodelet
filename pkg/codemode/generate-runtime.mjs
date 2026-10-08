import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';

const wasm = readFileSync(new URL(import.meta.resolve('quickjs-wasi/quickjs.wasm')));
const expected = 'd4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9';
const actual = createHash('sha256').update(wasm).digest('hex');
if (actual !== expected) {
  throw new Error(`QuickJS WASM checksum mismatch: expected ${expected}, got ${actual}`);
}

writeFileSync(new URL('./runtime_quickjs.wasm', import.meta.url), wasm);
console.log('Generated runtime_quickjs.wasm from the checksum-verified npm dependency');
