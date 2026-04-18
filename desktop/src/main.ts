import { spawn, type ChildProcessByStdio } from 'node:child_process';
import fs from 'node:fs';
import type { Readable } from 'node:stream';
import net from 'node:net';
import path from 'node:path';
import {
  BrowserWindow,
  Menu,
  type MenuItemConstructorOptions,
  app,
  dialog,
  shell,
} from 'electron';

import { getRemoteDisplayLabel, normalizeRemoteServerURL } from './lib/remote';
import { canOpenExternalURL } from './lib/navigation';
import { buildSidecarArgs, resolveSidecarBinary } from './lib/sidecar';
import {
  type DesktopState,
  loadDesktopState,
  resolveInitialWorkspace,
  saveDesktopState,
  shouldConnectToRemote,
} from './lib/state';

const launchTimeoutMs = 30000;
const readinessPollIntervalMs = 250;

let mainWindow: BrowserWindow | null = null;
let sidecarProcess: ChildProcessByStdio<null, Readable, Readable> | null = null;
let currentBaseUrl = '';
let desktopState: DesktopState = {
  connectionMode: 'local',
  workspacePath: '',
  remoteUrl: '',
};
let shuttingDown = false;

const desktopVersion = resolveDesktopVersion();

if (!app.requestSingleInstanceLock()) {
  app.quit();
}

app.on('second-instance', () => {
  if (!mainWindow) {
    return;
  }

  if (mainWindow.isMinimized()) {
    mainWindow.restore();
  }

  mainWindow.focus();
});

function getProjectRoot(): string {
  return path.resolve(app.getAppPath(), '..', '..');
}

function resolveDesktopVersion(): string {
  const candidatePaths = app.isPackaged
    ? [path.join(process.resourcesPath, 'VERSION.txt')]
    : [
        path.resolve(__dirname, '..', '..', 'VERSION.txt'),
        path.resolve(app.getAppPath(), '..', 'VERSION.txt'),
      ];

  for (const candidatePath of candidatePaths) {
    try {
      const version = fs.readFileSync(candidatePath, 'utf8').trim();
      if (version) {
        return version;
      }
    } catch {
      // Try the next candidate.
    }
  }

  return app.getVersion();
}

function withDefaultDesktopPath(existingPath: string | undefined): string {
  const entries = [
    '/opt/homebrew/bin',
    '/usr/local/bin',
    '/usr/bin',
    '/bin',
    '/usr/sbin',
    '/sbin',
    ...(existingPath || '').split(path.delimiter),
  ].filter(Boolean);

  return [...new Set(entries)].join(path.delimiter);
}

function getCurrentOrigin(): string {
  if (!currentBaseUrl) {
    return '';
  }

  return new URL(currentBaseUrl).origin;
}

function isAllowedNavigation(url: string): boolean {
  if (url.startsWith('data:')) {
    return true;
  }

  const currentOrigin = getCurrentOrigin();
  if (!currentOrigin) {
    return false;
  }

  try {
    return new URL(url).origin === currentOrigin;
  } catch {
    return false;
  }
}

function openExternalURL(url: string): void {
  if (!canOpenExternalURL(url)) {
    return;
  }

  void shell.openExternal(url);
}

function attachNavigationGuards(window: BrowserWindow): void {
  window.webContents.setWindowOpenHandler(({ url }) => {
    if (isAllowedNavigation(url)) {
      return { action: 'allow' };
    }

    openExternalURL(url);
    return { action: 'deny' };
  });

  window.webContents.on('will-navigate', (event, url) => {
    if (isAllowedNavigation(url)) {
      return;
    }

    event.preventDefault();
    openExternalURL(url);
  });
}

function createMenu(): void {
  const template: MenuItemConstructorOptions[] = [];

  if (process.platform === 'darwin') {
    template.push({
      role: 'appMenu',
      submenu: [
        {
          label: `About Kodelet ${desktopVersion}`,
          click: () => {
            app.showAboutPanel();
          },
        },
        { type: 'separator' },
        { role: 'services' },
        { type: 'separator' },
        { role: 'hide' },
        { role: 'hideOthers' },
        { role: 'unhide' },
        { type: 'separator' },
        { role: 'quit' },
      ],
    });
  }

  const fileMenu: MenuItemConstructorOptions = {
    label: 'File',
    submenu: [
      {
        label: 'Open Local Workspace…',
        accelerator: 'CmdOrCtrl+O',
        click: () => {
          void promptForWorkspace();
        },
      },
      {
        label: 'Connect to Remote Server…',
        accelerator: 'CmdOrCtrl+Shift+O',
        click: () => {
          void promptForRemoteConnection();
        },
      },
      {
        label: 'Reconnect Current Server',
        accelerator: 'CmdOrCtrl+Shift+R',
        click: () => {
          void reconnectCurrentServer();
        },
      },
      { type: 'separator' },
      { role: 'quit' },
    ],
  };

  const editSubmenu: MenuItemConstructorOptions[] = [
    { role: 'undo' },
    { role: 'redo' },
    { type: 'separator' },
    { role: 'cut' },
    { role: 'copy' },
    { role: 'paste' },
  ];

  if (process.platform === 'darwin') {
    editSubmenu.push(
      { role: 'pasteAndMatchStyle' },
      { role: 'delete' },
      { role: 'selectAll' },
      { type: 'separator' },
      {
        label: 'Speech',
        submenu: [{ role: 'startSpeaking' }, { role: 'stopSpeaking' }],
      },
    );
  } else {
    editSubmenu.push({ role: 'delete' }, { type: 'separator' }, { role: 'selectAll' });
  }

  const editMenu: MenuItemConstructorOptions = {
    label: 'Edit',
    submenu: editSubmenu,
  };

  const viewMenu: MenuItemConstructorOptions = {
    label: 'View',
    submenu: [
      { role: 'reload' },
      { role: 'forceReload' },
      { role: 'toggleDevTools' },
      { type: 'separator' },
      { role: 'resetZoom' },
      { role: 'zoomIn' },
      { role: 'zoomOut' },
    ],
  };

  const windowMenu: MenuItemConstructorOptions = {
    label: 'Window',
    submenu: [{ role: 'minimize' }, { role: 'close' }],
  };

  template.push(fileMenu, editMenu, viewMenu, windowMenu);

  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

app.setAboutPanelOptions({
  applicationName: 'Kodelet',
  applicationVersion: desktopVersion,
  version: desktopVersion,
  copyright: 'Copyright © Jingkai He',
  website: 'https://github.com/jingkaihe/kodelet',
});

async function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        server.close(() => reject(new Error('failed to determine free port')));
        return;
      }

      const { port } = address;
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }

        resolve(port);
      });
    });
  });
}

async function waitForChatSettingsReady(
  baseUrl: string,
  childProcess?: ChildProcessByStdio<null, Readable, Readable> | null,
): Promise<void> {
  const deadline = Date.now() + launchTimeoutMs;
  const endpoint = `${baseUrl}/api/chat/settings`;

  while (Date.now() < deadline) {
    if (childProcess && childProcess.exitCode !== null) {
      throw new Error(`kodelet sidecar exited early with code ${childProcess.exitCode}`);
    }

    try {
      const response = await fetch(endpoint, {
        signal: AbortSignal.timeout(1500),
      });

      if (response.ok) {
        return;
      }
    } catch {
      // Keep polling until timeout.
    }

    await new Promise((resolve) => setTimeout(resolve, readinessPollIntervalMs));
  }

  throw new Error(`timed out waiting for ${endpoint}`);
}

function logSidecarStream(prefix: string, stream: NodeJS.ReadableStream): void {
  stream.setEncoding('utf8');
  stream.on('data', (chunk: string | Buffer) => {
    const text = typeof chunk === 'string' ? chunk : chunk.toString('utf8');
    for (const line of text.split(/\r?\n/)) {
      if (!line.trim()) {
        continue;
      }

      console.log(`${prefix} ${line}`);
    }
  });
}

async function stopSidecar(
  childProcess: ChildProcessByStdio<null, Readable, Readable> | null = sidecarProcess,
): Promise<void> {
  if (!childProcess) {
    return;
  }

  if (sidecarProcess === childProcess) {
    sidecarProcess = null;
    currentBaseUrl = '';
  }

  await new Promise<void>((resolve) => {
    let settled = false;
    const finish = (): void => {
      if (settled) {
        return;
      }

      settled = true;
      resolve();
    };

    const forceKillTimer = setTimeout(() => {
      if (childProcess.exitCode === null) {
        childProcess.kill('SIGKILL');
      }
      finish();
    }, 3000);

    childProcess.once('exit', () => {
      clearTimeout(forceKillTimer);
      finish();
    });

    if (childProcess.exitCode !== null) {
      clearTimeout(forceKillTimer);
      finish();
      return;
    }

    childProcess.kill('SIGTERM');
  });
}

async function connectToRemote(remoteInput: string): Promise<void> {
  const remoteUrl = normalizeRemoteServerURL(remoteInput);
  const previousSidecar = sidecarProcess;
  const previousBaseUrl = currentBaseUrl;

  await waitForChatSettingsReady(remoteUrl);

  currentBaseUrl = remoteUrl;
  sidecarProcess = null;

  try {
    if (mainWindow) {
      mainWindow.setTitle(`Kodelet — Remote: ${getRemoteDisplayLabel(remoteUrl)}`);
      await mainWindow.loadURL(remoteUrl);
    }
  } catch (error) {
    currentBaseUrl = previousBaseUrl;
    sidecarProcess = previousSidecar;
    throw error;
  }

  desktopState = saveDesktopState(app.getPath('userData'), {
    ...desktopState,
    connectionMode: 'remote',
    remoteUrl,
  });
  await stopSidecar(previousSidecar);
}

async function launchWorkspace(workspacePath: string): Promise<void> {
  const previousSidecar = sidecarProcess;
  const previousBaseUrl = currentBaseUrl;
  const port = await findFreePort();
  const sidecarBinary = resolveSidecarBinary({
    argv: process.argv,
    isPackaged: app.isPackaged,
    projectRoot: getProjectRoot(),
    resourcesPath: process.resourcesPath,
  });
  const baseUrl = `http://127.0.0.1:${port}`;

  const childProcess = spawn(
    sidecarBinary,
    buildSidecarArgs({
      host: '127.0.0.1',
      port,
      workspace: workspacePath,
    }),
    {
      cwd: workspacePath,
      env: {
        ...process.env,
        KODELET_DESKTOP: '1',
        PATH: withDefaultDesktopPath(process.env.PATH),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let launchSettled = false;
  let rejectLaunchError: (error: Error) => void = () => {};
  const launchErrorPromise = new Promise<never>((_resolve, reject) => {
    rejectLaunchError = reject;
  });

  childProcess.on('error', (error) => {
    if (!launchSettled) {
      rejectLaunchError(new Error(`failed to start kodelet sidecar "${sidecarBinary}": ${error.message}`));
      return;
    }

    if (sidecarProcess !== childProcess || shuttingDown) {
      return;
    }

    showErrorDialog('Kodelet sidecar error', error);
  });

  logSidecarStream('[kodelet]', childProcess.stdout);
  logSidecarStream('[kodelet]', childProcess.stderr);

  childProcess.once('exit', (code, signal) => {
    if (sidecarProcess !== childProcess) {
      return;
    }

    sidecarProcess = null;
    currentBaseUrl = '';

    if (shuttingDown) {
      return;
    }

    dialog.showErrorBox('Kodelet stopped', `The local Kodelet sidecar exited unexpectedly.\n\ncode=${code ?? 'null'} signal=${signal ?? 'null'}`);
  });

  try {
    await Promise.race([waitForChatSettingsReady(baseUrl, childProcess), launchErrorPromise]);
    launchSettled = true;
  } catch (error) {
    launchSettled = true;
    childProcess.kill('SIGTERM');
    throw error;
  }

  sidecarProcess = childProcess;
  currentBaseUrl = baseUrl;

  try {
    if (mainWindow) {
      mainWindow.setTitle(`Kodelet — Local: ${workspacePath}`);
      await mainWindow.loadURL(baseUrl);
    }
  } catch (error) {
    sidecarProcess = previousSidecar;
    currentBaseUrl = previousBaseUrl;
    childProcess.kill('SIGTERM');
    throw error;
  }

  desktopState = saveDesktopState(app.getPath('userData'), {
    ...desktopState,
    connectionMode: 'local',
    workspacePath,
  });
  await stopSidecar(previousSidecar);
}

async function reconnectCurrentServer(): Promise<void> {
  try {
    if (shouldConnectToRemote(desktopState)) {
      await connectToRemote(desktopState.remoteUrl);
      return;
    }

    await launchWorkspace(resolveInitialWorkspace(desktopState));
  } catch (error) {
    showErrorDialog('Failed to reconnect', error);
  }
}

async function promptForWorkspace(): Promise<void> {
  if (!mainWindow) {
    return;
  }

  const result = await dialog.showOpenDialog(mainWindow, {
    title: 'Choose a workspace for Kodelet',
    defaultPath: resolveInitialWorkspace(desktopState),
    properties: ['openDirectory'],
  });

  if (result.canceled || result.filePaths.length === 0) {
    return;
  }

  try {
    await launchWorkspace(result.filePaths[0]);
  } catch (error) {
    showErrorDialog('Failed to open local workspace', error);
  }
}

async function promptForRemoteConnection(): Promise<void> {
  try {
    if (!mainWindow) {
      return;
    }

    const input = await promptForText(
      'Enter the base URL of a remote Kodelet server',
      desktopState.remoteUrl || 'https://',
    );

    if (input === null) {
      return;
    }

    await connectToRemote(input);
  } catch (error) {
    showErrorDialog('Failed to connect to remote server', error);
  }
}

async function promptForText(message: string, defaultValue: string): Promise<string | null> {
  if (!mainWindow) {
    return null;
  }

  const modal = new BrowserWindow({
    parent: mainWindow,
    modal: true,
    width: 560,
    height: 240,
    resizable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    show: false,
    useContentSize: true,
    title: 'Connect to Remote Server',
    backgroundColor: '#ece5d8',
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  modal.removeMenu();
  modal.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));

  return new Promise<string | null>((resolve, reject) => {
    let settled = false;

    const finish = (value: string | null): void => {
      if (settled) {
        return;
      }

      settled = true;
      if (!modal.isDestroyed()) {
        modal.close();
      }
      resolve(value);
    };

    modal.on('closed', () => {
      if (!settled) {
        settled = true;
        resolve(null);
      }
    });

    modal.webContents.on('will-navigate', (event, url) => {
      if (!url.startsWith('kodelet-input://')) {
        return;
      }

      event.preventDefault();

      const parsed = new URL(url);
      if (parsed.hostname === 'cancel') {
        finish(null);
        return;
      }

      if (parsed.hostname === 'submit') {
        finish(parsed.searchParams.get('value') ?? '');
      }
    });

    modal.webContents.on('did-fail-load', (_event, errorCode, errorDescription) => {
      reject(new Error(`failed to load remote connection dialog (${errorCode}): ${errorDescription}`));
    });

    modal.webContents.on('dom-ready', () => {
      void modal.webContents.executeJavaScript(
        `(() => {
          const root = document.documentElement;
          const body = document.body;
          return {
            width: Math.ceil(Math.max(root.scrollWidth, body.scrollWidth, root.clientWidth, body.clientWidth)),
            height: Math.ceil(Math.max(root.scrollHeight, body.scrollHeight, root.clientHeight, body.clientHeight))
          };
        })()`,
        true,
      ).then((size: { width: number; height: number }) => {
        const width = Math.min(Math.max(size.width, 560), 720);
        const height = Math.min(Math.max(size.height, 280), 420);
        modal.setContentSize(width, height);
        modal.center();
      }).catch(() => {
        modal.setContentSize(620, 320);
        modal.center();
      });
    });

    modal.once('ready-to-show', () => {
      modal.show();
    });

    void modal.loadURL(`data:text/html;charset=UTF-8,${encodeURIComponent(renderTextInputDialogHTML(message, defaultValue))}`);
  });
}

function renderTextInputDialogHTML(message: string, defaultValue: string): string {
  return `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Connect to Remote Server</title>
    <style>
      @import url('https://fonts.googleapis.com/css2?family=Lora:wght@400;500;600&family=Poppins:wght@500;600;700&display=swap');

      :root {
        color-scheme: light;
        --kodelet-dark: #141413;
        --kodelet-light: #faf9f5;
        --kodelet-mid-gray: #b0aea5;
        --kodelet-light-gray: #e8e6dc;
        --kodelet-orange: #d97757;
        --kodelet-blue: #6a9bcc;
        --kodelet-shell: #ece5d8;
        --kodelet-panel: rgba(245, 240, 231, 0.94);
        --kodelet-panel-soft: rgba(238, 232, 221, 0.78);
        --font-heading: "Poppins", "Helvetica Neue", Arial, sans-serif;
        --font-body: "Lora", Georgia, serif;
        --noise-texture:
          radial-gradient(circle at 20% 20%, rgba(255,255,255,0.22) 0, rgba(255,255,255,0) 38%),
          radial-gradient(circle at 78% 0%, rgba(217,119,87,0.08) 0, rgba(217,119,87,0) 32%),
          radial-gradient(circle at 100% 100%, rgba(106,155,204,0.08) 0, rgba(106,155,204,0) 30%);
      }

      * { box-sizing: border-box; }

      body {
        margin: 0;
        min-height: 100vh;
        padding: 0;
        background:
          linear-gradient(180deg, rgba(250, 249, 245, 0.96), rgba(236, 229, 216, 0.98)),
          var(--noise-texture);
        color: var(--kodelet-dark);
        font: 14px/1.5 var(--font-body);
        overflow: hidden;
      }

      main {
        position: relative;
        min-height: 100vh;
        overflow: hidden;
        padding: 1.35rem 1.5rem 1.25rem;
      }

      main::before {
        content: "";
        position: absolute;
        inset: 0;
        pointer-events: none;
        background:
          radial-gradient(circle at top right, rgba(106, 155, 204, 0.12), transparent 34%),
          radial-gradient(circle at left center, rgba(217, 119, 87, 0.08), transparent 30%);
      }

      h1 {
        position: relative;
        margin: 0 0 0.35rem;
        font-family: var(--font-heading);
        font-size: clamp(1.25rem, 2vw, 1.65rem);
        font-weight: 600;
        letter-spacing: -0.03em;
      }

      p {
        position: relative;
        margin: 0 0 0.95rem;
        font-size: 0.92rem;
        line-height: 1.5;
        color: rgba(20, 20, 19, 0.66);
      }

      .surface-panel {
        position: relative;
        display: flex;
        flex-direction: column;
        gap: 0.75rem;
        min-width: 0;
      }

      form {
        display: flex;
        min-width: 0;
        flex-direction: column;
        gap: 0.75rem;
      }

      input {
        min-height: 2.75rem;
        width: 100%;
        border-radius: 1rem;
        border: 1px solid rgba(20, 20, 19, 0.1);
        background: rgba(247, 243, 235, 0.56);
        padding: 0.78rem 0.92rem;
        font-family: var(--font-heading);
        font-size: 0.78rem;
        font-weight: 600;
        color: var(--kodelet-dark);
        box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.72);
      }

      input:focus {
        outline: none;
        border-color: rgba(217, 119, 87, 0.32);
        box-shadow: 0 0 0 4px rgba(217, 119, 87, 0.1);
      }

      .actions {
        display: flex;
        justify-content: flex-end;
        gap: 0.65rem;
        margin-top: 0.25rem;
        flex-wrap: wrap;
      }

      button {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-height: 2.65rem;
        border-radius: 1.2rem;
        border: 1px solid rgba(20, 20, 19, 0.1);
        padding: 0.58rem 1rem;
        background: rgba(247, 243, 235, 0.72);
        color: var(--kodelet-dark);
        font-family: var(--font-heading);
        font-size: 0.72rem;
        font-weight: 600;
        letter-spacing: 0.08em;
        text-transform: uppercase;
        cursor: pointer;
        box-shadow:
          inset 0 1px 0 rgba(255, 255, 255, 0.72),
          0 10px 26px rgba(20, 20, 19, 0.08);
        transition:
          background-color 160ms ease,
          box-shadow 160ms ease,
          transform 160ms ease,
          color 160ms ease;
      }

      button:hover {
        transform: translateY(-1px);
        box-shadow:
          inset 0 1px 0 rgba(255, 255, 255, 0.88),
          0 14px 32px rgba(20, 20, 19, 0.12);
      }

      button:focus-visible {
        outline: none;
        box-shadow:
          0 0 0 4px rgba(217, 119, 87, 0.1),
          inset 0 1px 0 rgba(255, 255, 255, 0.88),
          0 14px 32px rgba(20, 20, 19, 0.12);
      }

      button.primary {
        border-color: transparent;
        background: var(--kodelet-blue);
        color: white;
        box-shadow:
          inset 0 1px 0 rgba(255, 255, 255, 0.12),
          0 10px 26px rgba(20, 20, 19, 0.12);
      }

      button.primary:hover {
        box-shadow:
          inset 0 1px 0 rgba(255, 255, 255, 0.16),
          0 14px 32px rgba(20, 20, 19, 0.16);
      }

      .eyebrow {
        position: relative;
        margin: 0 0 0.35rem;
        font-family: var(--font-heading);
        font-size: 0.72rem;
        font-weight: 600;
        line-height: 1.2;
        letter-spacing: 0.12em;
        text-transform: uppercase;
        color: #7a756b;
      }

      .hint {
        position: relative;
        margin: 0;
        font-size: 0.79rem;
        line-height: 1.45;
        color: rgba(20, 20, 19, 0.58);
      }

      code {
        font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
        font-size: 0.95em;
        color: rgba(20, 20, 19, 0.74);
      }

      @media (max-width: 640px) {
        body {
          padding: 0;
        }

        main {
          padding: 1.1rem 1.1rem 1rem;
        }

        .actions {
          justify-content: stretch;
        }

        .actions button {
          flex: 1 1 0;
        }
      }
    </style>
  </head>
  <body>
    <main>
      <div class="surface-panel">
        <div class="eyebrow">Remote connection</div>
        <h1>Connect to remote server</h1>
        <p>${escapeHTML(message)}</p>
        <form id="remote-form">
        <input id="remote-url" type="text" value="${escapeHTML(defaultValue)}" spellcheck="false" autofocus />
        <p class="hint">Use the root origin of a running <code>kodelet serve</code> instance, for example <code>https://kodelet.example.com</code>.</p>
        <div class="actions">
          <button id="cancel" type="button">Cancel</button>
          <button class="primary" type="submit">Connect</button>
        </div>
        </form>
      </div>
    </main>
    <script>
      const input = document.getElementById('remote-url');
      const form = document.getElementById('remote-form');
      const cancel = document.getElementById('cancel');
      window.addEventListener('DOMContentLoaded', () => {
        input.focus();
        input.select();
      });
      form.addEventListener('submit', (event) => {
        event.preventDefault();
        window.location.href = 'kodelet-input://submit?value=' + encodeURIComponent(input.value);
      });
      cancel.addEventListener('click', () => {
        window.location.href = 'kodelet-input://cancel';
      });
      window.addEventListener('keydown', (event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          window.location.href = 'kodelet-input://cancel';
        }
      });
    </script>
  </body>
</html>`;
}

function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function showErrorDialog(title: string, error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  dialog.showErrorBox(title, message);
}

async function connectInitialTarget(): Promise<void> {
  if (shouldConnectToRemote(desktopState)) {
    await connectToRemote(desktopState.remoteUrl);
    return;
  }

  await launchWorkspace(resolveInitialWorkspace(desktopState));
}

async function createMainWindow(): Promise<void> {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 1024,
    minHeight: 720,
    title: 'Kodelet',
    backgroundColor: '#0b0f19',
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  attachNavigationGuards(mainWindow);

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

void app.whenReady().then(async () => {
  desktopState = loadDesktopState(app.getPath('userData'));
  createMenu();
  await createMainWindow();

  try {
    await connectInitialTarget();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    dialog.showErrorBox('Failed to launch Kodelet', message);
  }

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length !== 0) {
      return;
    }

    void createMainWindow().then(async () => {
      if (currentBaseUrl && mainWindow) {
        await mainWindow.loadURL(currentBaseUrl);
      }
    });
  });
});

app.on('before-quit', (event) => {
  if (shuttingDown) {
    return;
  }

  shuttingDown = true;
  event.preventDefault();
  void stopSidecar().finally(() => {
    app.quit();
  });
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});
