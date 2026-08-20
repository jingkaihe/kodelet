export const TERMINAL_POP_OUT_STORAGE_KEY = 'kodelet.terminal.pop-out';
const TERMINAL_POP_OUT_CHANNEL_NAME = 'kodelet-terminal-pop-out';
const TERMINAL_POP_OUT_SESSION_ID_KEY = 'kodelet.terminal.pop-out.id';
export const TERMINAL_POP_OUT_HEARTBEAT_INTERVAL = 1500;
export const TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD = 2500;

export interface TerminalPopOutTarget {
  cwd: string;
  conversationId?: string;
}

export interface TerminalPopOutRecord extends TerminalPopOutTarget {
  id: string;
  state?: 'active' | 'closing';
  updatedAt: number;
  version: 2;
}

interface TerminalPopOutStore {
  records: TerminalPopOutRecord[];
  version: 1;
}

export type TerminalPopOutMessage =
  | { type: 'probe' }
  | { type: 'active'; record: TerminalPopOutRecord }
  | { type: 'closing'; id: string; cwd: string; conversationId?: string };

export const getTerminalPopOutTargetKey = (target: TerminalPopOutTarget): string => {
  const conversationId = target.conversationId?.trim();
  return conversationId ? `conversation:${conversationId}` : `cwd:${target.cwd}`;
};

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

const isTerminalPopOutRecord = (value: unknown): value is TerminalPopOutRecord => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  const record = value as Partial<TerminalPopOutRecord>;
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

    const store: TerminalPopOutStore = { records, version: 1 };
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
    const storedRecords = isTerminalPopOutRecord(parsed)
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

    const validRecords = storedRecords.filter(isTerminalPopOutRecord);
    const retainedRecords = validRecords.filter(
      (record) =>
        record.state !== 'closing' ||
        Date.now() - record.updatedAt <= TERMINAL_POP_OUT_RELOAD_GRACE_PERIOD
    );
    if (
      retainedRecords.length !== storedRecords.length ||
      isTerminalPopOutRecord(parsed)
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
  target: TerminalPopOutTarget
): TerminalPopOutRecord | null =>
  readTerminalPopOutRecords()
    .filter((record) => getTerminalPopOutTargetKey(record) === getTerminalPopOutTargetKey(target))
    .sort((left, right) => right.updatedAt - left.updatedAt)[0] ?? null;

export const readTerminalPopOutRecord = (cwd?: string): TerminalPopOutRecord | null =>
  cwd === undefined
    ? readTerminalPopOutRecords().sort((left, right) => right.updatedAt - left.updatedAt)[0] ?? null
    : readTerminalPopOutRecordForTarget({ cwd });

export const readTerminalPopOutRecordById = (id: string): TerminalPopOutRecord | null =>
  readTerminalPopOutRecords().find((record) => record.id === id) ?? null;

export const writeTerminalPopOutRecord = (record: TerminalPopOutRecord): void => {
  const recordTargetKey = getTerminalPopOutTargetKey(record);
  const records = readTerminalPopOutRecords().filter(
    (currentRecord) =>
      currentRecord.id !== record.id &&
      getTerminalPopOutTargetKey(currentRecord) !== recordTargetKey
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
    cwd?: unknown;
    conversationId?: unknown;
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
    typeof message.cwd === 'string' &&
    (message.conversationId === undefined ||
      (typeof message.conversationId === 'string' && message.conversationId.trim() !== ''))
  );
};
