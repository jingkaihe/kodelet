const MAX_OSC_PAYLOAD = 8192;

/** Shell prompt titles (user@host:path, or Debian's user@host: path) report a directory. */
export const terminalShellDirectory = (title: string): string | null =>
  /^[^@\s/:]*@[^@\s/:]+:\s*(~(?:\/.*)?|\/.*)$/u.exec(title.trim())?.[1] ?? null;

const directoryFromOSC = (payload: string | null): string | null => {
  if (payload === null || /\p{Cc}/u.test(payload)) return null;
  const match = /^7;file:\/\/[^/?#\\]*(\/[^?#\\]*)$/i.exec(payload);
  if (!match) return null;

  try {
    // Validate the authority too, but retain the original path: URL normalizes
    // dot segments, which may have a different meaning in a symlinked path.
    if (new URL(payload.slice(2)).protocol !== 'file:') return null;
    return decodeURIComponent(match[1]);
  } catch {
    return null;
  }
};

/** Track OSC 7 directories and OSC 0/2 titles in stream order, including replay. */
export const createTerminalDirectoryTracker = (
  onDirectory: (cwd: string) => void,
  onTitle?: (title: string) => void
): ((data: Uint8Array) => void) => {
  const decoder = new TextDecoder();
  let state: 'text' | 'escape' | 'osc' | 'string' = 'text';
  let escaped = false;
  let payload: string | null = '';
  let payloadLength = 0;
  let overlong = false;

  const finish = () => {
    const directory = state === 'osc' ? directoryFromOSC(payload) : null;
    const title =
      state === 'osc' && payload !== null && !/\p{Cc}/u.test(payload) && /^[02];/.test(payload)
        ? payload.slice(2)
        : null;
    state = 'text';
    payload = '';
    if (directory !== null) onDirectory(directory);
    if (title !== null) onTitle?.(title);
  };

  return (data) => {
    for (const char of decoder.decode(data, { stream: true })) {
      // CAN and SUB cancel an unfinished control sequence.
      if (char === '\x18' || char === '\x1a') {
        state = 'text';
        payload = '';
        escaped = false;
      } else if (state === 'text') {
        if (char === '\x1b') state = 'escape';
      } else if (state === 'escape') {
        if (char === ']') {
          state = 'osc';
          payload = '';
          payloadLength = 0;
          overlong = false;
        } else if ('PX^_'.includes(char)) {
          // DCS, SOS, PM and APC can contain escaped OSC-looking data.
          state = 'string';
        } else if (char !== '\x1b') {
          state = 'text';
        }
      } else if (escaped) {
        escaped = false;
        if (char === '\\') {
          finish();
        } else if (state === 'osc') {
          // An oversized, unterminated OSC must not swallow future updates.
          if (overlong && char === ']') {
            payload = '';
            payloadLength = 0;
            overlong = false;
          } else {
            payload = null;
          }
        }
      } else if (char === '\x1b') {
        escaped = true;
      } else if (state === 'osc') {
        if (char === '\x07') {
          finish();
        } else if (!overlong) {
          payloadLength += char.length;
          if (payloadLength > MAX_OSC_PAYLOAD) {
            payload = null;
            overlong = true;
          } else if (payload !== null) {
            payload += char;
          }
        }
      }
    }
  };
};
