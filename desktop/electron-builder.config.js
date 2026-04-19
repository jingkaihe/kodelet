const path = require('node:path');

const projectDir = __dirname;
const repoRoot = path.resolve(projectDir, '..');
const sidecarDir = process.env.KODELET_SIDECAR_DIR
  ? path.resolve(process.env.KODELET_SIDECAR_DIR)
  : path.join(repoRoot, 'bin');

module.exports = {
  appId: 'com.jingkaihe.kodelet.desktop',
  productName: 'Kodelet',
  copyright: 'Copyright © Jingkai He',
  artifactName: '${productName}-${version}-${os}-${arch}.${ext}',
  icon: 'assets/icon.png',
  directories: {
    output: 'dist',
    buildResources: 'assets',
  },
  files: ['build/**/*', 'package.json'],
  extraResources: [
    {
      from: sidecarDir,
      to: 'bin',
      filter: ['kodelet*'],
    },
    {
      from: path.join(repoRoot, 'VERSION.txt'),
      to: '.',
      filter: ['VERSION.txt'],
    },
  ],
  mac: {
    category: 'public.app-category.developer-tools',
    icon: 'assets/icon.icns',
    target: ['dir', 'zip'],
  },
  linux: {
    category: 'Development',
    icon: 'assets/icon.png',
    target: ['AppImage', 'tar.gz'],
  },
  win: {
    icon: 'assets/icon.ico',
    target: ['portable'],
  },
};
