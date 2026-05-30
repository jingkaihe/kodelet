#!/usr/bin/env node

import path from "node:path";
import { pathToFileURL } from "node:url";
import nodeProcess from "node:process";

import { runExtension } from "../runtime.js";
import type { ExtensionEntrypoint } from "../types.js";

const modulePath = nodeProcess.argv[2];

if (!modulePath) {
  nodeProcess.stderr.write("usage: kodelet-extension-node <module>\n");
  nodeProcess.exit(2);
}

const resolvedPath = path.isAbsolute(modulePath) ? modulePath : path.resolve(nodeProcess.cwd(), modulePath);
const mod = (await import(pathToFileURL(resolvedPath).href)) as { default?: ExtensionEntrypoint };

if (typeof mod.default !== "function") {
  nodeProcess.stderr.write(`extension module ${modulePath} must export a default function\n`);
  nodeProcess.exit(2);
}

await runExtension(mod.default);
