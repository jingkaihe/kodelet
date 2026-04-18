import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export type ConnectionMode = 'local' | 'remote';

export interface DesktopState {
  connectionMode: ConnectionMode;
  workspacePath: string;
  remoteUrl: string;
}

export function loadDesktopState(userDataPath: string): DesktopState {
  const statePath = getStateFilePath(userDataPath);

  try {
    const raw = fs.readFileSync(statePath, 'utf8');
    return normalizeState(JSON.parse(raw) as Partial<DesktopState>);
  } catch {
    return normalizeState({});
  }
}

export function saveDesktopState(userDataPath: string, state: Partial<DesktopState>): DesktopState {
  const nextState = normalizeState(state);
  fs.mkdirSync(userDataPath, { recursive: true });
  fs.writeFileSync(getStateFilePath(userDataPath), `${JSON.stringify(nextState, null, 2)}\n`, 'utf8');
  return nextState;
}

export function resolveInitialWorkspace(state: Partial<DesktopState>): string {
  const candidate = typeof state.workspacePath === 'string' ? state.workspacePath.trim() : '';
  if (candidate && fs.existsSync(candidate)) {
    try {
      if (fs.statSync(candidate).isDirectory()) {
        return candidate;
      }
    } catch {
      // Fall back to home directory below.
    }
  }

  return os.homedir();
}

export function shouldConnectToRemote(state: Partial<DesktopState>): boolean {
  return state.connectionMode === 'remote' && typeof state.remoteUrl === 'string' && state.remoteUrl.trim() !== '';
}

export function getStateFilePath(userDataPath: string): string {
  return path.join(userDataPath, 'desktop-state.json');
}

function normalizeState(state: Partial<DesktopState>): DesktopState {
  return {
    connectionMode: state.connectionMode === 'remote' ? 'remote' : 'local',
    workspacePath: typeof state.workspacePath === 'string' ? state.workspacePath.trim() : '',
    remoteUrl: typeof state.remoteUrl === 'string' ? state.remoteUrl.trim() : '',
  };
}

