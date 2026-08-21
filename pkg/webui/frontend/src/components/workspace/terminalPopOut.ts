import type { WorkspaceTarget } from '../../types';

export const TERMINAL_POP_OUT_STORAGE_KEY = 'kodelet.terminal.pop-out';
const TERMINAL_POP_OUT_CHANNEL_NAME = 'kodelet-terminal-pop-out';
const TERMINAL_POP_OUT_SESSION_ID_KEY = 'kodelet.terminal.pop-out.id';
export const TERMINAL_POP_OUT_HEARTBEAT_INTERVAL = 1500;
export const TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD = 2500;

export interface TerminalPopOutRecord {
  id: string;
  target: WorkspaceTarget;
  state?: 'active' | 'closing';
  updatedAt: number;
  version: 3;
}

export interface LegacyTerminalPopOutRecord {
  id: string;
  cwd: string;
  conversationId?: string;
  state?: 'active' | 'closing';
  updatedAt: number;
  version: 2;
}

export type StoredTerminalPopOutRecord = TerminalPopOutRecord | LegacyTerminalPopOutRecord;

interface TerminalPopOutStore {
  records: StoredTerminalPopOutRecord[];
  version: 1;
}

export type TerminalPopOutMessage =
  | { type: 'probe' }
  | { type: 'active'; record: StoredTerminalPopOutRecord }
  | { type: 'closing'; id: string; target: WorkspaceTarget }
  | { type: 'closing'; id: string; cwd: string; conversationId?: string };

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
    record.version === 3 &&
    typeof record.id === 'string' &&
    isWorkspaceTarget(record.target) &&
    (record.state === undefined ||
      record.state === 'active' ||
      record.state === 'closing') &&
    typeof record.updatedAt === 'number' &&
    Number.isFinite(record.updatedAt)
  );
};

const isLegacyTerminalPopOutRecord = (value: unknown): value is LegacyTerminalPopOutRecord => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const record = value as Partial<LegacyTerminalPopOutRecord>;
  return (
    record.version === 2 &&
    typeof record.id === 'string' &&
    typeof record.cwd === 'string' &&
    (record.conversationId === undefined ||
      (typeof record.conversationId === 'string' && record.conversationId.trim() !== '')) &&
    (record.state === undefined ||
      record.state === 'active' ||
      record.state === 'closing') &&
    typeof record.updatedAt === 'number' &&
    Number.isFinite(record.updatedAt)
  );
};

const isStoredTerminalPopOutRecord = (value: unknown): value is StoredTerminalPopOutRecord =>
  isTerminalPopOutRecord(value) || isLegacyTerminalPopOutRecord(value);

const legacyTerminalPopOutTargetMatches = (
  legacy: Pick<LegacyTerminalPopOutRecord, 'cwd' | 'conversationId'>,
  target: WorkspaceTarget
): boolean => {
  const conversationId = legacy.conversationId?.trim();
  if (conversationId) {
    return target.kind === 'runner' && target.conversationId === conversationId;
  }
  return target.kind === 'local' && (target.cwd || '') === legacy.cwd;
};

export const terminalPopOutRecordMatchesTarget = (
  record: StoredTerminalPopOutRecord,
  target: WorkspaceTarget
): boolean =>
  isTerminalPopOutRecord(record)
    ? getTerminalPopOutTargetKey(record.target) === getTerminalPopOutTargetKey(target)
    : legacyTerminalPopOutTargetMatches(record, target);

const normalizeTerminalPopOutRecord = (
  record: StoredTerminalPopOutRecord,
  target?: WorkspaceTarget
): TerminalPopOutRecord | null => {
  if (isTerminalPopOutRecord(record)) {
    return record;
  }
  if (target && legacyTerminalPopOutTargetMatches(record, target)) {
    return {
      id: record.id,
      target:
        target.kind === 'runner'
          ? { ...target, conversationId: record.conversationId || target.conversationId }
          : { kind: 'local', cwd: record.cwd || target.cwd },
      state: record.state,
      updatedAt: record.updatedAt,
      version: 3,
    };
  }
  if (!record.conversationId) {
    return {
      id: record.id,
      target: { kind: 'local', cwd: record.cwd || undefined },
      state: record.state,
      updatedAt: record.updatedAt,
      version: 3,
    };
  }
  return null;
};

const removeStoredRecords = (): void => {
  try {
    window.localStorage.removeItem(TERMINAL_POP_OUT_STORAGE_KEY);
  } catch {
    // Storage may be unavailable; BroadcastChannel and the live window reference still work.
  }
};

const writeTerminalPopOutRecords = (records: StoredTerminalPopOutRecord[]): void => {
  try {
    if (records.length === 0) {
      removeStoredRecords();
      return;
    }

    const store: TerminalPopOutStore = { records, version: 1 };
    window.localStorage.setItem(TERMINAL_POP_OUT_STORAGE_KEY, JSON.stringify(store));
  } catch {
    // The in-memory window reference and BroadcastChannel remain available.
  }
};

const readTerminalPopOutRecords = (): StoredTerminalPopOutRecord[] => {
  try {
    const rawStore = window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY);
    if (!rawStore) {
      return [];
    }

    const parsed = JSON.parse(rawStore) as unknown;
    const storedRecords = isStoredTerminalPopOutRecord(parsed)
      ? [parsed]
      : parsed &&
          typeof parsed === 'object' &&
          !Array.isArray(parsed) &&
          (parsed as Partial<TerminalPopOutStore>).version === 1 &&
          Array.isArray((parsed as Partial<TerminalPopOutStore>).records)
        ? (parsed as Partial<TerminalPopOutStore>).records ?? []
        : null;
    if (!storedRecords) {
      removeStoredRecords();
      return [];
    }

    const validRecords = storedRecords.filter(isStoredTerminalPopOutRecord);
    const retainedRecords = validRecords.filter(
      (record) =>
        record.state !== 'closing' ||
        Date.now() - record.updatedAt <= TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD
    );
    if (
      retainedRecords.length !== storedRecords.length ||
      isStoredTerminalPopOutRecord(parsed)
    ) {
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
): TerminalPopOutRecord | null => {
  const record = readTerminalPopOutRecords()
    .filter((storedRecord) => terminalPopOutRecordMatchesTarget(storedRecord, target))
    .sort((left, right) => right.updatedAt - left.updatedAt)[0];
  return record ? normalizeTerminalPopOutRecord(record, target) : null;
};

export const readTerminalPopOutRecord = (cwd?: string): TerminalPopOutRecord | null => {
  if (cwd !== undefined) {
    return readTerminalPopOutRecordForTarget({ kind: 'local', cwd });
  }
  for (const record of readTerminalPopOutRecords().sort(
    (left, right) => right.updatedAt - left.updatedAt
  )) {
    const normalized = normalizeTerminalPopOutRecord(record);
    if (normalized) {
      return normalized;
    }
  }
  return null;
};

export const readTerminalPopOutRecordById = (
  id: string,
  target?: WorkspaceTarget
): TerminalPopOutRecord | null => {
  const record = readTerminalPopOutRecords().find((storedRecord) => storedRecord.id === id);
  return record ? normalizeTerminalPopOutRecord(record, target) : null;
};

export const writeTerminalPopOutRecord = (record: TerminalPopOutRecord): void => {
  const records = readTerminalPopOutRecords().filter(
    (currentRecord) =>
      currentRecord.id !== record.id &&
      !terminalPopOutRecordMatchesTarget(currentRecord, record.target)
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
    cwd?: unknown;
    conversationId?: unknown;
  };
  if (message.type === 'probe') {
    return true;
  }
  if (message.type === 'active') {
    return isStoredTerminalPopOutRecord(message.record);
  }
  return (
    message.type === 'closing' &&
    typeof message.id === 'string' &&
    (isWorkspaceTarget(message.target) ||
      (typeof message.cwd === 'string' &&
        (message.conversationId === undefined ||
          (typeof message.conversationId === 'string' &&
            message.conversationId.trim() !== ''))))
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
    return terminalPopOutRecordMatchesTarget(message.record, target);
  }
  if ('target' in message && isWorkspaceTarget(message.target)) {
    return getTerminalPopOutTargetKey(message.target) === getTerminalPopOutTargetKey(target);
  }
  return 'cwd' in message && legacyTerminalPopOutTargetMatches(message, target);
};
