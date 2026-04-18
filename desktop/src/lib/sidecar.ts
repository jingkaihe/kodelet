import fs from 'node:fs';
import path from 'node:path';

export interface ResolveSidecarBinaryOptions {
  argv: string[];
  isPackaged: boolean;
  projectRoot: string;
  resourcesPath: string;
}

export interface SidecarLaunchOptions {
  host: string;
  port: number;
  workspace: string;
}

export function resolveSidecarBinary(options: ResolveSidecarBinaryOptions): string {
  const overridePath = getKodeletPathOverride(options.argv);
  if (overridePath) {
    const resolvedOverride = path.resolve(overridePath);
    if (!fs.existsSync(resolvedOverride)) {
      throw new Error(`Configured kodelet binary does not exist: ${resolvedOverride}`);
    }
    return resolvedOverride;
  }

  if (options.isPackaged) {
    const packagedBinary = findExistingBinary(getPackagedBinaryCandidates(options.resourcesPath));
    if (packagedBinary) {
      return packagedBinary;
    }
  }

  return 'kodelet';
}

export function buildSidecarArgs(options: SidecarLaunchOptions): string[] {
  return [
    'serve',
    '--host',
    options.host,
    '--port',
    String(options.port),
    '--cwd',
    options.workspace,
  ];
}

function findExistingBinary(candidates: string[]): string {
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  return '';
}

function getDevelopmentBinaryCandidates(projectRoot: string): string[] {
  return [
    path.join(projectRoot, 'bin', 'kodelet'),
    path.join(projectRoot, 'bin', 'kodelet.exe'),
  ];
}

function getPackagedBinaryCandidates(resourcesPath: string): string[] {
  return [
    path.join(resourcesPath, 'bin', 'kodelet'),
    path.join(resourcesPath, 'bin', 'kodelet.exe'),
  ];
}

function getKodeletPathOverride(argv: string[]): string {
  for (let index = 0; index < argv.length; index += 1) {
    const value = argv[index];
    if (value === '--kodelet-path') {
      return argv[index + 1] || '';
    }

    if (value.startsWith('--kodelet-path=')) {
      return value.slice('--kodelet-path='.length);
    }
  }

  return '';
}
