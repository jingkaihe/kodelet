import { describe, expect, it, vi } from 'vitest';
import { createTerminalDirectoryTracker, terminalShellDirectory } from './terminalDirectory';

const encoder = new TextEncoder();
const osc = (payload: string, end = '\x07') => encoder.encode(`\x1b]${payload}${end}`);

describe('terminalShellDirectory', () => {
  it.each([
    ['user@fedora:~/workspace/kodelet', '~/workspace/kodelet'],
    ['@fedora:/srv/project', '/srv/project'],
    ['user@debian: ~/workspace', '~/workspace'],
    ['user@runner:~', '~'],
    ['user@runner:/tmp/project', '/tmp/project'],
    ['user@runner:~/my project/café:logs', '~/my project/café:logs'],
    ['  user@runner:~/workspace  ', '~/workspace'],
    ['(chroot)user@debian: /srv', '/srv'],
    ['nvim main.go', null],
    ['ssh user@runner:/tmp', null],
    ['user@runner:pytest', null],
  ])('extracts only shell prompt directories from %j', (title, expected) => {
    expect(terminalShellDirectory(title)).toBe(expected);
  });
});

describe('createTerminalDirectoryTracker', () => {
  it.each([
    ['0', '\x07'],
    ['0', '\x1b\\'],
    ['2', '\x07'],
    ['2', '\x1b\\'],
  ])('reports OSC %s titles across every byte split with terminator %j', (command, end) => {
    const title = 'vim; /home/用户/café 🦊';
    const data = osc(`${command};${title}`, end);
    for (let split = 0; split <= data.length; split++) {
      const onDirectory = vi.fn();
      const onTitle = vi.fn();
      const track = createTerminalDirectoryTracker(onDirectory, onTitle);
      track(data.subarray(0, split));
      if (split < data.length) expect(onTitle).not.toHaveBeenCalled();
      track(data.subarray(split));
      expect(onTitle.mock.calls).toEqual([[title]]);
      expect(onDirectory).not.toHaveBeenCalled();
    }
  });

  it('reports directory and program title updates in stream order', () => {
    const updates: string[] = [];
    const track = createTerminalDirectoryTracker(
      (cwd) => updates.push(`cwd:${cwd}`),
      (title) => updates.push(`title:${title}`)
    );
    track(
      encoder.encode(
        '\x1b]7;file:///project\x07' +
          '\x1b]0;user@runner:/project\x07' +
          '\x1b]2;nvim main.go\x1b\\' +
          '\x1b]7;file:///project/src\x07' +
          '\x1b]0;user@runner:/project/src\x07'
      )
    );
    expect(updates).toEqual([
      'cwd:/project',
      'title:user@runner:/project',
      'title:nvim main.go',
      'cwd:/project/src',
      'title:user@runner:/project/src',
    ]);
  });

  it('supports empty titles while ignoring icon-only, unrelated, malformed and oversized titles', () => {
    const onTitle = vi.fn();
    const track = createTerminalDirectoryTracker(vi.fn(), onTitle);
    for (const payload of [
      '1;icon title',
      '8;hyperlink',
      '52;clipboard',
      '02;invalid',
      '0;bad\nline',
      '2;bad\x00title',
      '2;bad\x1b[31m',
      `2;${'a'.repeat(8192)}`,
    ])
      track(osc(payload));
    track(encoder.encode('\x1bPtmux;\x1b\x1b]2;nested\x07\x1b\\'));
    expect(onTitle).not.toHaveBeenCalled();
    track(osc('0;'));
    track(osc('2;recovered'));
    expect(onTitle.mock.calls).toEqual([[''], ['recovered']]);
  });

  it.each(['\x07', '\x1b\\'])('handles every byte split with terminator %j', (end) => {
    const data = osc('7;file://runner/home/用户/🦊/my%20project', end);
    for (let split = 0; split <= data.length; split++) {
      const onDirectory = vi.fn();
      const track = createTerminalDirectoryTracker(onDirectory);
      track(data.subarray(0, split));
      if (split < data.length) expect(onDirectory).not.toHaveBeenCalled();
      track(data.subarray(split));
      expect(onDirectory.mock.calls).toEqual([['/home/用户/🦊/my project']]);
    }
  });

  it('handles byte-by-byte UTF-8 and consecutive updates among normal output', () => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    const data = encoder.encode(
      'prompt 🦊\x1b[32m\x1b]7;file:///home/用户\x07$ ls\r\n' +
        '\x1b]7;file://runner/tmp/café\x1b\\\x1b]7;file:///\x07'
    );
    for (const byte of data) {
      track(new Uint8Array([byte]));
      track(new Uint8Array());
    }
    expect(onDirectory.mock.calls).toEqual([['/home/用户'], ['/tmp/café'], ['/']]);
  });

  it.each([
    ['file:///home/my%20project/%E4%B8%AD%E6%96%87', '/home/my project/中文'],
    ['file://remote.example/a%2Fb/%23%3F%25%5C+/%2520', '/a/b/#?%\\+/%20'],
    ['file://[::1]/workspace', '/workspace'],
    ['FILE://localhost/', '/'],
    ['file:///symlink/../%2e/target', '/symlink/.././target'],
  ])('decodes the complete path from %s without normalizing it', (uri, cwd) => {
    const onDirectory = vi.fn();
    createTerminalDirectoryTracker(onDirectory)(osc(`7;${uri}`));
    expect(onDirectory.mock.calls).toEqual([[cwd]]);
  });

  it.each([
    '7;https://host/path',
    '7;file:/path',
    '7;file:relative',
    '7;file://host',
    '7;file://host/path%',
    '7;file://host/path%2',
    '7;file://host/path%GG',
    '7;file://host/path%FF',
    '7;file://ho%GGst/path',
    '7;file://[invalid]/path',
    '7;file://user@host/path',
    '7;file://host:123/path',
    '7;file://host?query/path',
    '7;file://host#fragment/path',
    '7;file://host\\other/path',
    '7;file://host/path?query',
    '7;file://host/path#fragment',
    '7;file://host/path?',
    '7;file://host/path#',
    '7;file://host/path\\child',
    '7;file://host/path\nchild',
    '7;file://host/path\tchild',
    '7;file://host/path\x00child',
    '7;file://host/path\x1b[31m',
    '07;file://host/path',
    '70;file://host/path',
    '7;not a URI',
  ])('ignores malformed or non-file OSC 7 payload %j', (payload) => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    track(osc(payload));
    expect(onDirectory).not.toHaveBeenCalled();
    track(osc('7;file:///recovered'));
    expect(onDirectory.mock.calls).toEqual([['/recovered']]);
  });

  it('ignores ordinary output and unrelated OSC sequences including titles', () => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    track(encoder.encode('file:///not-an-update\r\n7;file:///also-not-an-update'));
    for (const command of ['0', '2', '8', '52', '133']) {
      track(osc(`${command};file:///not-a-directory-update`));
    }
    track(osc('2;title containing \x1b]7;file:///nested'));
    expect(onDirectory).not.toHaveBeenCalled();
    track(osc('7;file:///actual'));
    expect(onDirectory.mock.calls).toEqual([['/actual']]);
  });

  it.each(['P', 'X', '^', '_'])('ignores OSC-looking data inside ESC %s strings', (kind) => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    const data = encoder.encode(
      '\x1b' +
        kind +
        'tmux;\x1b\x1b]7;file:///nested\x07' +
        '\x1b\x1b\\\x1b]7;file:///still-nested\x07\x1b\\' +
        '\x1b]7;file:///actual\x07'
    );
    for (const byte of data) track(new Uint8Array([byte]));
    expect(onDirectory.mock.calls).toEqual([['/actual']]);
  });

  it.each(['\x18', '\x1a'])('cancels incomplete control strings with %j', (cancel) => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    track(encoder.encode(`\x1b]7;file:///cancelled${cancel}\x07`));
    track(encoder.encode(`\x1bPunterminated${cancel}`));
    track(osc('7;file:///actual'));
    expect(onDirectory.mock.calls).toEqual([['/actual']]);
  });

  it('accepts a payload at the size limit and drops one beyond it', () => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    const payload = `7;file:///${'a'.repeat(8192 - '7;file:///'.length)}`;
    track(osc(payload));
    expect(onDirectory.mock.calls).toEqual([[payload.slice('7;file://'.length)]]);
    onDirectory.mockClear();
    track(osc(`${payload}a`));
    expect(onDirectory).not.toHaveBeenCalled();
    track(osc('7;file:///recovered'));
    expect(onDirectory.mock.calls).toEqual([['/recovered']]);
  });

  it.each(['\x07', '\x1b\\', ''])('recovers after an oversized OSC ending in %j', (end) => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    track(encoder.encode('\x1b]7;file:///'));
    for (let chunk = 0; chunk < 20; chunk++) track(encoder.encode('a'.repeat(1024)));
    track(encoder.encode(end));
    expect(onDirectory).not.toHaveBeenCalled();
    // Split the fresh introducer too, including when the oversized OSC had no terminator.
    track(encoder.encode('\x1b'));
    track(encoder.encode(']7;file:///recovered\x1b'));
    track(encoder.encode('\\'));
    expect(onDirectory.mock.calls).toEqual([['/recovered']]);
  });

  it('keeps partial sequences isolated between tracker instances', () => {
    const first = vi.fn();
    const second = vi.fn();
    const trackFirst = createTerminalDirectoryTracker(first);
    const trackSecond = createTerminalDirectoryTracker(second);
    trackFirst(encoder.encode('\x1b]7;file:///first'));
    trackSecond(osc('7;file:///second'));
    expect(first).not.toHaveBeenCalled();
    expect(second.mock.calls).toEqual([['/second']]);
    trackFirst(encoder.encode('\x07'));
    expect(first.mock.calls).toEqual([['/first']]);
  });

  it('resynchronizes even if an oversized payload was already malformed', () => {
    const onDirectory = vi.fn();
    const track = createTerminalDirectoryTracker(onDirectory);
    track(encoder.encode('\x1b]7;file:///bad\x1b[31m'));
    track(encoder.encode('a'.repeat(8192)));
    track(osc('7;file:///recovered'));
    expect(onDirectory.mock.calls).toEqual([['/recovered']]);
  });
});
