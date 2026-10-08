# Embedded QuickJS runtime

`runtime_quickjs.wasm` is generated from the unmodified `quickjs.wasm` in the published `quickjs-wasi@3.6.2` npm package. The WASM file and `node_modules` are gitignored; only the npm manifest, lockfile, generation script, and provenance are tracked. Node/npm are needed for generation, just as for the frontend; neither Node/npm nor a C toolchain is needed to run the compiled Kodelet binary. The Go adapter is owned by Kodelet; neither the npm JavaScript host nor its optional extensions run in Kodelet.

## Generation

From the repository root:

```bash
mise run codemode-generation  # Only the embedded QuickJS runtime
mise run code-generation      # Runtime plus frontend assets
```

Equivalently, `go generate ./pkg/codemode` runs `npm ci --ignore-scripts` and `npm run generate` in this directory. The install uses the exact version and tarball integrity in `package-lock.json`, without running dependency lifecycle scripts. `generate-runtime.mjs` verifies the extracted WASM's SHA-256 before writing `runtime_quickjs.wasm` for `go:embed`.

Normal `mise` build, build-dev, lint, and test tasks generate the runtime automatically. Release hooks and the Docker build also generate it. Run generation once before invoking raw `go build` or `go test` on a fresh checkout. Regeneration uses the npm cache when available; the first install needs access to the pinned package.

## Pins and integrity

- Package: `quickjs-wasi@3.6.2`.
- Upstream repository: `https://github.com/vercel-labs/quickjs-wasi`.
- Source revision: `5a7a0eeda87c99542f8cf3095b6d61ecfa755977` (version 3.6.2).
- QuickJS-NG submodule revision: `6d46d07d04041b40f4f49eaa7fdebe44c314c699`.
- Tarball: `https://registry.npmjs.org/quickjs-wasi/-/quickjs-wasi-3.6.2.tgz`.
- Tarball integrity: `sha512-FCqGtGOrMgzUiIrMNMA2YnsOxCNwo31dzqXvclXUC6xeT35NJLKXQJsvbeCTjvoFAwZgEAPg8U6+KAPDGXn8Mg==`.
- WASM SHA-256: `d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9`.
- Host dependency: `github.com/tetratelabs/wazero v1.12.0`, verified through `go.sum`.

`TestRuntimeArtifactIntegrity` also checks the embedded digest in the Go tests. To update the runtime, review the new upstream package, update the exact dependency and lockfile, and update the reviewed SHA-256 in `generate-runtime.mjs` and the integrity test together with these pins and licenses. Upstream provides its build recipe in `Makefile` using WASI SDK 32 and optional Binaryen optimization; regeneration from the pinned published artifact, rather than an unpinned local compiler rebuild, is the deterministic artifact update procedure.

The binary imports only the upstream host callback/module-loader stubs and six WASI clock/random/descriptor functions. Kodelet supplies no preopened filesystem, inherited environment, host streams, network, or optional extensions. Modules have a hard linear-memory limit and are interrupted when their context expires. The QuickJS native host callback uses reentrant C value-marshalling exports on the VM owner only; it never invokes guest callbacks, drains jobs, waits for tool work, or enters the VM from a worker.

## Licenses

### quickjs-wasi interface (MIT)

Copyright (c) 2026 Vercel, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### QuickJS-NG (MIT)

Copyright (c) 2017-2026 Fabrice Bellard

Copyright (c) 2017-2024 Charlie Gordon

Copyright (c) 2023-2026 Ben Noordhuis

Copyright (c) 2023-2026 Saúl Ibarra Corretgé

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
