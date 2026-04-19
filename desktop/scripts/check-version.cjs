const fs = require('node:fs');
const path = require('node:path');

const projectDir = path.resolve(__dirname, '..');
const repoRoot = path.resolve(projectDir, '..');
const versionPath = path.join(repoRoot, 'VERSION.txt');
const packagePath = path.join(projectDir, 'package.json');
const lockPath = path.join(projectDir, 'package-lock.json');

const hasOwn = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
const errors = [];

const version = fs.readFileSync(versionPath, 'utf8').trim();
if (version.length === 0) {
  errors.push('VERSION.txt must not be empty');
}

if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
  errors.push(`VERSION.txt must contain a SemVer version, got "${version}"`);
}

const packageJson = JSON.parse(fs.readFileSync(packagePath, 'utf8'));
if (hasOwn(packageJson, 'version')) {
  errors.push('desktop/package.json must not set version; electron-builder derives it from VERSION.txt');
}

const packageLock = JSON.parse(fs.readFileSync(lockPath, 'utf8'));
if (hasOwn(packageLock, 'version')) {
  errors.push('desktop/package-lock.json must not set the root version');
}

if (packageLock.packages && packageLock.packages[''] && hasOwn(packageLock.packages[''], 'version')) {
  errors.push('desktop/package-lock.json packages[""] must not set version');
}

const builderConfig = require(path.join(projectDir, 'electron-builder.config.js'));
if (!builderConfig.extraMetadata || builderConfig.extraMetadata.version !== version) {
  errors.push('electron-builder.config.js must expose extraMetadata.version from VERSION.txt');
}

if (errors.length > 0) {
  console.error(errors.join('\n'));
  process.exit(1);
}

console.log(`Desktop version source OK (${version})`);
