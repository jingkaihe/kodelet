import type { WorkspaceTarget } from '../../types';

export const TERMINAL_POP_OUT_STORAGE_KEY = 'kodelet.terminal.pop-out';
const TERMINAL_POP_OUT_CHANNEL_NAME = 'kodelet-terminal-pop-out';
const TERMINAL_POP_OUT_SESSION_ID_KEY = 'kodelet.terminal.pop-out.id';
const TERMINAL_POP_OUT_STORAGE_VERSION = 2;
export const TERMINAL_POP_OUT_HEARTBEAT_INTERVAL = 1500;
export const TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD = 2500;

export interface TerminalPopOutRecord {
  id: string;
  target: WorkspaceTarget;
  state?: 'active' | 'closing';
  updatedAt: number;
}

interface TerminalPopOutStore {
  records: TerminalPopOutRecord[];
  version: typeof TERMINAL_POP_OUT_STORAGE_VERSION;
}

export type TerminalPopOutMessage =
  | { type: 'probe' }
  | { type: 'active'; record: TerminalPopOutRecord }
  | { type: 'closing'; id: string; target: WorkspaceTarget };

export const getTerminalPopOutTargetKey = (target: WorkspaceTarget): string =>
  target.kind === 'runner' ? `runner:${target.runnerId}` : `local:${target.cwd || ''}`;

export const createTerminalPopOutId = (): string =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `terminal-pop-out-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

export const getTerminalPopOutSessionId = (): string => {
  try {
    const existingId = window.sessionStorage.getItem(TERMINAL_POP_OUT_SESSION_ID_KEY);
    if (existingId) {
      return existingId;
    }

    const id = createTerminalPopOutId();
    window.sessionStorage.setItem(TERMINAL_POP_OUT_SESSION_ID_KEY, id);
    return id;
  } catch {
    return createTerminalPopOutId();
  }
};

const isWorkspaceTarget = (value: unknown): value is WorkspaceTarget => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const target = value as Partial<WorkspaceTarget>;
  if (target.kind === 'local') {
    return target.cwd === undefined || typeof target.cwd === 'string';
  }
  return (
    target.kind === 'runner' &&
    typeof target.runnerId === 'string' &&
    target.runnerId.trim() !== '' &&
    (target.conversationId === undefined ||
      (typeof target.conversationId === 'string' && target.conversationId.trim() !== ''))
  );
};

const isTerminalPopOutRecord = (value: unknown): value is TerminalPopOutRecord => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const record = value as Partial<TerminalPopOutRecord>;
  return (
    typeof record.id === 'string' &&
    isWorkspaceTarget(record.target) &&
    (record.state === undefined ||
      record.state === 'active' ||
      record.state === 'closing') &&
    typeof record.updatedAt === 'number' &&
    Number.isFinite(record.updatedAt)
  );
};

const isTerminalPopOutStore = (value: unknown): value is TerminalPopOutStore => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const store = value as Partial<TerminalPopOutStore>;
  return store.version === TERMINAL_POP_OUT_STORAGE_VERSION && Array.isArray(store.records);
};

const removeStoredRecords = (): void => {
  try {
    window.localStorage.removeItem(TERMINAL_POP_OUT_STORAGE_KEY);
  } catch {
    // Storage may be unavailable; BroadcastChannel and the live window reference still work.
  }
};

const writeTerminalPopOutRecords = (records: TerminalPopOutRecord[]): void => {
  try {
    if (records.length === 0) {
      removeStoredRecords();
      return;
    }

    const store: TerminalPopOutStore = {
      records,
      version: TERMINAL_POP_OUT_STORAGE_VERSION,
    };
    window.localStorage.setItem(TERMINAL_POP_OUT_STORAGE_KEY, JSON.stringify(store));
  } catch {
    // The in-memory window reference and BroadcastChannel remain available.
  }
};

const readTerminalPopOutRecords = (): TerminalPopOutRecord[] => {
  try {
    const rawStore = window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY);
    if (!rawStore) {
      return [];
    }

    const parsed = JSON.parse(rawStore) as unknown;
    if (!isTerminalPopOutStore(parsed)) {
      removeStoredRecords();
      return [];
    }

    const validRecords = parsed.records.filter(isTerminalPopOutRecord);
    const retainedRecords = validRecords.filter(
      (record) =>
        record.state !== 'closing' ||
        Date.now() - record.updatedAt <= TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD
    );
    if (retainedRecords.length !== parsed.records.length) {
      writeTerminalPopOutRecords(retainedRecords);
    }

    return retainedRecords;
  } catch {
    removeStoredRecords();
    return [];
  }
};

export const readTerminalPopOutRecordForTarget = (
  target: WorkspaceTarget
): TerminalPopOutRecord | null =>
  readTerminalPopOutRecords()
    .filter(
      (record) => getTerminalPopOutTargetKey(record.target) === getTerminalPopOutTargetKey(target)
    )
    .sort((left, right) => right.updatedAt - left.updatedAt)[0] ?? null;

export const readTerminalPopOutRecord = (cwd?: string): TerminalPopOutRecord | null =>
  cwd === undefined
    ? readTerminalPopOutRecords().sort((left, right) => right.updatedAt - left.updatedAt)[0] ?? null
    : readTerminalPopOutRecordForTarget({ kind: 'local', cwd });

export const readTerminalPopOutRecordById = (id: string): TerminalPopOutRecord | null =>
  readTerminalPopOutRecords().find((record) => record.id === id) ?? null;

export const writeTerminalPopOutRecord = (record: TerminalPopOutRecord): void => {
  const recordTargetKey = getTerminalPopOutTargetKey(record.target);
  const records = readTerminalPopOutRecords().filter(
    (currentRecord) =>
      currentRecord.id !== record.id &&
      getTerminalPopOutTargetKey(currentRecord.target) !== recordTargetKey
  );
  writeTerminalPopOutRecords([...records, record]);
};

export const clearTerminalPopOutRecord = (id: string): void => {
  writeTerminalPopOutRecords(
    readTerminalPopOutRecords().filter((record) => record.id !== id)
  );
};

export const createTerminalPopOutChannel = (): BroadcastChannel | null => {
  if (typeof BroadcastChannel === 'undefined') {
    return null;
  }

  try {
    return new BroadcastChannel(TERMINAL_POP_OUT_CHANNEL_NAME);
  } catch {
    return null;
  }
};

export const isTerminalPopOutMessage = (value: unknown): value is TerminalPopOutMessage => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const message = value as {
    type?: unknown;
    record?: unknown;
    id?: unknown;
    target?: unknown;
  };
  if (message.type === 'probe') {
    return true;
  }
  if (message.type === 'active') {
    return isTerminalPopOutRecord(message.record);
  }
  return (
    message.type === 'closing' &&
    typeof message.id === 'string' &&
    isWorkspaceTarget(message.target)
  );
};

export const terminalPopOutMessageMatchesTarget = (
  message: TerminalPopOutMessage,
  target: WorkspaceTarget
): boolean => {
  if (message.type === 'probe') {
    return false;
  }
  if (message.type === 'active') {
    return getTerminalPopOutTargetKey(message.record.target) === getTerminalPopOutTargetKey(target);
  }
  return getTerminalPopOutTargetKey(message.target) === getTerminalPopOutTargetKey(target);
};
