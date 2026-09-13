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
    vi.useFakeTimers();
    document.body.innerHTML = '';
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
    document.body.innerHTML = '';
  });

  it('removes the toast and empty stack after three seconds', () => {
    showToast('Test message', 'success');

    const toast = document.querySelector('.kodelet-toast');
    expect(toast).toHaveTextContent('Test message');

    vi.advanceTimersByTime(2999);
    expect(toast).toBeInTheDocument();

    vi.advanceTimersByTime(1);
    expect(toast).not.toBeInTheDocument();
    expect(document.querySelector('.kodelet-toasts')).not.toBeInTheDocument();
  });

  it.each([
    ['info', 'i', 'status'],
    ['success', '✓', 'status'],
    ['error', '!', 'alert'],
    ['neutral', '·', 'status'],
  ] as const)('renders the %s variant with its marker and live role', (type, marker, role) => {
    showToast('Notification message', type);

    const toast = document.querySelector('.kodelet-toast');
    expect(toast).toHaveAttribute('data-type', type);
    expect(toast).toHaveAttribute('role', role);
    expect(toast).toHaveAttribute('aria-atomic', 'true');
    expect(toast?.querySelector('.toast-marker')).toHaveTextContent(marker);
    expect(toast?.querySelector('.toast-marker')).toHaveAttribute('aria-hidden', 'true');
    expect(toast?.querySelector('button')).toHaveAccessibleName('Dismiss notification');
  });

  it('defaults to info and omits an empty title', () => {
    showToast('Notification message', undefined, '  ');

    expect(document.querySelector('.kodelet-toast')).toHaveAttribute('data-type', 'info');
    expect(document.querySelector('.toast-title')).not.toBeInTheDocument();
  });

  it('stacks simultaneous notifications with independent expiration', () => {
    showToast('First notification');
    vi.advanceTimersByTime(1000);
    showToast('Second notification');

    const stack = document.querySelector('.kodelet-toasts');
    expect(document.querySelectorAll('.kodelet-toasts')).toHaveLength(1);
    expect(stack).toHaveAccessibleName('Notifications');
    expect(stack?.children).toHaveLength(2);

    vi.advanceTimersByTime(2000);
    expect(stack?.children).toHaveLength(1);
    expect(stack).toHaveTextContent('Second notification');
    expect(stack).not.toHaveTextContent('First notification');

    vi.advanceTimersByTime(1000);
    expect(stack).not.toBeInTheDocument();
  });

  it('dismisses individual notifications and clears their timers', () => {
    showToast('First notification');
    showToast('Second notification');

    const buttons = document.querySelectorAll<HTMLButtonElement>('.toast-dismiss');
    buttons[0].click();

    expect(document.querySelectorAll('.kodelet-toast')).toHaveLength(1);
    expect(document.querySelector('.kodelet-toast')).toHaveTextContent('Second notification');
    expect(vi.getTimerCount()).toBe(1);

    buttons[1].click();
    expect(document.querySelector('.kodelet-toasts')).not.toBeInTheDocument();
    expect(vi.getTimerCount()).toBe(0);

    showToast('New notification');
    expect(document.querySelector('.kodelet-toast')).toHaveTextContent('New notification');
  });

  it('pauses expiration while hovered and resumes the remaining time', () => {
    showToast('Read at your own pace');
    const toast = document.querySelector('.kodelet-toast');

    vi.advanceTimersByTime(1000);
    toast?.dispatchEvent(new MouseEvent('mouseenter'));
    vi.advanceTimersByTime(10000);
    expect(toast).toBeInTheDocument();

    toast?.dispatchEvent(new MouseEvent('mouseleave'));
    vi.advanceTimersByTime(1999);
    expect(toast).toBeInTheDocument();
    vi.advanceTimersByTime(1);
    expect(toast).not.toBeInTheDocument();
  });

  it('pauses expiration while keyboard focus is inside the notification', () => {
    showToast('Keyboard-accessible notification');
    const toast = document.querySelector('.kodelet-toast');
    const dismiss = toast?.querySelector('button');

    vi.advanceTimersByTime(1000);
    dismiss?.focus();
    expect(dismiss).toHaveFocus();
    vi.advanceTimersByTime(10000);
    expect(toast).toBeInTheDocument();

    dismiss?.blur();
    vi.advanceTimersByTime(1999);
    expect(toast).toBeInTheDocument();
    vi.advanceTimersByTime(1);
    expect(toast).not.toBeInTheDocument();
  });

  it('waits until both hover and focus leave before resuming expiration', () => {
    showToast('Keep visible during interaction');
    const toast = document.querySelector('.kodelet-toast');
    const dismiss = toast?.querySelector('button');

    vi.advanceTimersByTime(1000);
    toast?.dispatchEvent(new MouseEvent('mouseenter'));
    dismiss?.focus();
    toast?.dispatchEvent(new MouseEvent('mouseleave'));
    vi.advanceTimersByTime(5000);
    expect(toast).toBeInTheDocument();

    toast?.dispatchEvent(new MouseEvent('mouseenter'));
    dismiss?.blur();
    vi.advanceTimersByTime(5000);
    expect(toast).toBeInTheDocument();

    toast?.dispatchEvent(new MouseEvent('mouseleave'));
    vi.advanceTimersByTime(2000);
    expect(toast).not.toBeInTheDocument();
  });

  it('preserves long and multiline notification text', () => {
    const message =
      'Workspace extension started with a very-long-token-that-should-wrap-instead-of-overflowing-the-notification-box\nReady for the next task.';
    showToast(message);

    expect(document.querySelector('.toast-message')?.textContent).toBe(message);
  });

  it('escapes notification titles and messages as text', () => {
    const title = '<img src=x onerror="alert(1)">';
    const message = '<script>alert("unsafe")</script> & <b>not markup</b>';
    showToast(message, 'info', title);

    expect(document.querySelector('.toast-title')?.textContent).toBe(title);
    expect(document.querySelector('.toast-message')?.textContent).toBe(message);
    expect(document.querySelector('.kodelet-toast')?.querySelector('img, script, b')).toBeNull();
  });

  it('uses only the new toast markup', () => {
    showToast('Notification message', 'success');

    expect(
      document.querySelector('.toast, .kodelet-toast-card, [class*="alert-"]')
    ).not.toBeInTheDocument();
    expect(document.querySelector('.kodelet-toast')?.parentElement).toBe(
      document.querySelector('.kodelet-toasts')
    );
  });

  it('renders notification title and message separately', () => {
    showToast(
      'Workspace extension started. Remembered bash policy: 0 allowed, 0 denied.',
      'info',
      '  Workspace extension ready  '
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
