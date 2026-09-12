import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Usage } from '../types';
import {
  cn,
  copyToClipboard,
  debounce,
  detectLanguageFromPath,
  escapeHtml,
  escapeUrl,
  formatCompactRelativeTime,
  formatCost,
  formatDuration,
  formatFileSize,
  showToast,
  truncateText,
} from './index';

describe('formatCompactRelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns em dash for missing values', () => {
    expect(formatCompactRelativeTime(null)).toBe('—');
    expect(formatCompactRelativeTime(undefined)).toBe('—');
    expect(formatCompactRelativeTime('')).toBe('—');
  });

  it('returns intuitive text for recent timestamps', () => {
    vi.setSystemTime(new Date('2023-01-02T12:00:30Z'));
    expect(formatCompactRelativeTime('2023-01-02T12:00:00Z')).toBe('just now');
  });

  it('returns short minute and hour labels with suffixes', () => {
    vi.setSystemTime(new Date('2023-01-02T12:35:00Z'));
    expect(formatCompactRelativeTime('2023-01-02T12:32:00Z')).toBe('3m ago');
    expect(formatCompactRelativeTime('2023-01-02T10:00:00Z')).toBe('2h ago');
  });

  it('returns short day labels and compact dates for older timestamps', () => {
    vi.setSystemTime(new Date('2023-01-10T12:00:00Z'));
    expect(formatCompactRelativeTime('2023-01-08T12:00:00Z')).toBe('2d ago');
    expect(formatCompactRelativeTime('2022-12-20T12:00:00Z')).toBe('Dec 20');
  });
});

describe('formatCost', () => {
  it('returns $0.00 for null or undefined usage', () => {
    expect(formatCost(null)).toBe('$0.00');
    expect(formatCost(undefined)).toBe('$0.00');
  });

  it('calculates total cost from all usage types', () => {
    const usage: Usage = {
      inputCost: 0.001,
      outputCost: 0.002,
      cacheCreationCost: 0.0005,
      cacheReadCost: 0.0001,
    };
    expect(formatCost(usage)).toBe('$0.0036');
  });

  it('handles missing cost properties', () => {
    const usage: Usage = {
      inputCost: 0.001,
    };
    expect(formatCost(usage)).toBe('$0.0010');
  });
});

describe('copyToClipboard', () => {
  let originalClipboard: Clipboard | undefined;
  let originalExecCommand: typeof document.execCommand | undefined;

  beforeEach(() => {
    originalClipboard = navigator.clipboard;
    originalExecCommand = document.execCommand;

    // Mock navigator.clipboard
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn(),
      },
    });
  });

  afterEach(() => {
    Object.assign(navigator, {
      clipboard: originalClipboard,
    });

    if (originalExecCommand) {
      Object.defineProperty(document, 'execCommand', {
        value: originalExecCommand,
        configurable: true,
        writable: true,
      });
      return;
    }

    // @ts-expect-error execCommand is optional in jsdom tests
    delete document.execCommand;
  });

  it('calls clipboard writeText with provided text', async () => {
    const writeTextMock = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();

    await copyToClipboard('test text');

    expect(writeTextMock).toHaveBeenCalledWith('test text');
  });

  it('falls back to execCommand when clipboard API is unavailable', async () => {
    Object.assign(navigator, {
      clipboard: undefined,
    });
    const execCommandMock = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, 'execCommand', {
      value: execCommandMock,
      configurable: true,
      writable: true,
    });

    await copyToClipboard('test text');

    expect(execCommandMock).toHaveBeenCalledWith('copy');
  });

  it('falls back to execCommand when clipboard API errors', async () => {
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('Failed'));
    const execCommandMock = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, 'execCommand', {
      value: execCommandMock,
      configurable: true,
      writable: true,
    });

    await copyToClipboard('test text');

    expect(execCommandMock).toHaveBeenCalledWith('copy');
  });

  it('shows an error when both clipboard mechanisms fail', async () => {
    Object.assign(navigator, {
      clipboard: undefined,
    });
    const execCommandMock = vi.fn().mockReturnValue(false);
    Object.defineProperty(document, 'execCommand', {
      value: execCommandMock,
      configurable: true,
      writable: true,
    });
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    await copyToClipboard('test text');

    expect(consoleErrorSpy).toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });
});

describe('showToast', () => {
  beforeEach(() => {
    // Clear any existing toasts before each test
    document.body.innerHTML = '';
  });

  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('creates and removes toast element', () => {
    vi.useFakeTimers();

    showToast('Test message', 'success');

    const toast = document.querySelector('.toast');
    expect(toast).toBeTruthy();
    expect(toast?.innerHTML).toContain('alert-success');
    // Check for the actual text content, not innerHTML
    expect(toast?.textContent).toContain('Test message');

    vi.advanceTimersByTime(3000);

    expect(document.querySelector('.toast')).toBeFalsy();

    vi.useRealTimers();
  });

  it('supports neutral toasts for muted feedback', () => {
    showToast('Conversation deleted', 'neutral');

    const toast = document.querySelector('.toast');
    expect(toast).toBeTruthy();
    expect(toast?.innerHTML).toContain('alert-neutral');
    expect(toast?.textContent).toContain('Conversation deleted');
  });

  it('wraps long notification messages inside the toast body', () => {
    showToast(
      'Workspace extension started with a very-long-token-that-should-wrap-instead-of-overflowing-the-notification-box',
      'info'
    );

    const message = document.querySelector('.toast-message');
    expect(message).toBeTruthy();
    expect(message?.textContent).toContain('very-long-token');
  });

  it('renders notification title and message separately', () => {
    showToast(
      'Workspace extension started. Remembered bash policy: 0 allowed, 0 denied.',
      'info',
      'Workspace extension ready'
    );

    expect(document.querySelector('.toast-title')?.textContent).toBe('Workspace extension ready');
    expect(document.querySelector('.toast-message')?.textContent).toContain(
      'Remembered bash policy'
    );
  });
});

describe('escapeHtml', () => {
  it('escapes HTML special characters', () => {
    expect(escapeHtml('<div>Test & "quotes"</div>')).toBe(
      '&lt;div&gt;Test &amp; "quotes"&lt;/div&gt;'
    );
  });

  it('returns empty string for falsy values', () => {
    expect(escapeHtml('')).toBe('');
  });
});

describe('escapeUrl', () => {
  it('returns empty string for falsy values', () => {
    expect(escapeUrl('')).toBe('');
  });

  it('allows http and https URLs', () => {
    expect(escapeUrl('http://example.com')).toBe('http://example.com');
    expect(escapeUrl('https://example.com')).toBe('https://example.com');
  });

  it('allows file URLs', () => {
    expect(escapeUrl('file:///path/to/file')).toBe('file:///path/to/file');
  });

  it('returns # for invalid URLs', () => {
    expect(escapeUrl('not a url')).toBe('#');
  });

  it('returns # for disallowed protocols', () => {
    expect(escapeUrl('javascript:alert(1)')).toBe('#');
    expect(escapeUrl('data:text/html,<script>alert(1)</script>')).toBe('#');
  });
});

describe('formatFileSize', () => {
  it('returns empty string for falsy values', () => {
    expect(formatFileSize(0)).toBe('');
  });

  it('formats bytes correctly', () => {
    expect(formatFileSize(100)).toBe('100.0 B');
    expect(formatFileSize(1024)).toBe('1.0 KB');
    expect(formatFileSize(1048576)).toBe('1.0 MB');
    expect(formatFileSize(1073741824)).toBe('1.0 GB');
  });

  it('handles decimal values', () => {
    expect(formatFileSize(1536)).toBe('1.5 KB');
  });
});

describe('formatDuration', () => {
  it('returns string as-is', () => {
    expect(formatDuration('1.5s')).toBe('1.5s');
  });

  it('converts nanoseconds to seconds', () => {
    expect(formatDuration(2500000000)).toBe('2.500s');
  });

  it('converts nanoseconds to milliseconds', () => {
    expect(formatDuration(150000000)).toBe('150ms');
  });

  it('shows sub-millisecond durations clearly', () => {
    expect(formatDuration(500000)).toBe('<1ms');
  });
});

describe('detectLanguageFromPath', () => {
  it('returns empty string for falsy values', () => {
    expect(detectLanguageFromPath('')).toBe('');
  });

  it('detects common languages', () => {
    expect(detectLanguageFromPath('file.js')).toBe('javascript');
    expect(detectLanguageFromPath('file.ts')).toBe('typescript');
    expect(detectLanguageFromPath('file.py')).toBe('python');
    expect(detectLanguageFromPath('file.go')).toBe('go');
  });

  it('returns extension for unknown languages', () => {
    expect(detectLanguageFromPath('file.xyz')).toBe('xyz');
  });

  it('handles paths with multiple dots', () => {
    expect(detectLanguageFromPath('my.file.test.js')).toBe('javascript');
  });
});

describe('debounce', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('delays function execution', () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);

    debounced('test');
    expect(fn).not.toHaveBeenCalled();

    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledWith('test');
  });

  it('cancels previous calls', () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);

    debounced('first');
    vi.advanceTimersByTime(50);
    debounced('second');
    vi.advanceTimersByTime(100);

    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith('second');
  });

  it('cancels pending calls', () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);

    debounced('test');
    debounced.cancel();
    vi.advanceTimersByTime(100);

    expect(fn).not.toHaveBeenCalled();
  });
});

describe('cn', () => {
  it('combines class names', () => {
    expect(cn('foo', 'bar')).toBe('foo bar');
  });

  it('filters out falsy values', () => {
    expect(cn('foo', null, undefined, false, 'bar', '')).toBe('foo bar');
  });

  it('returns empty string for all falsy values', () => {
    expect(cn(null, undefined, false, '')).toBe('');
  });
});

describe('truncateText', () => {
  it('returns original text if shorter than max length', () => {
    expect(truncateText('short', 10)).toBe('short');
  });

  it('truncates and adds ellipsis for long text', () => {
    expect(truncateText('very long text here', 10)).toBe('very long ...');
  });
});
