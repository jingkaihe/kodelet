import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  formatDate,
  formatCost,
  copyToClipboard,
  showToast,
  escapeHtml,
  escapeUrl,
  formatFileSize,
  formatDuration,
  detectLanguageFromPath,
  getFileIcon,
  debounce,
  throttle,
  deepClone,
  cn,
  highlightSearchTerm,
  truncateText,
  isImageFile,
  formatTimestamp,
} from './index';
import { Usage } from '../types';

describe('formatDate', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns N/A for null, undefined, or empty string', () => {
    expect(formatDate(null)).toBe('N/A');
    expect(formatDate(undefined)).toBe('N/A');
    expect(formatDate('')).toBe('N/A');
  });

  it('shows relative time for dates less than 24 hours ago', () => {
    const now = new Date('2023-01-02T12:00:00Z');
    vi.setSystemTime(now);

    const twoHoursAgo = '2023-01-02T10:00:00Z';
    expect(formatDate(twoHoursAgo)).toMatch(/ago$/); // Should end with 'ago'
  });

  it('shows formatted date for dates more than 24 hours ago', () => {
    const now = new Date('2023-01-10T12:00:00Z');
    vi.setSystemTime(now);

    const pastDate = '2023-01-02T10:00:00Z';
    expect(formatDate(pastDate)).toMatch(/Jan 2, 2023/);
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
  beforeEach(() => {
    // Mock navigator.clipboard
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn(),
      },
    });
  });

  it('calls clipboard writeText with provided text', async () => {
    const writeTextMock = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();

    await copyToClipboard('test text');

    expect(writeTextMock).toHaveBeenCalledWith('test text');
  });

  it('handles clipboard API errors', async () => {
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('Failed'));
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
});

describe('escapeHtml', () => {
  it('escapes HTML special characters', () => {
    expect(escapeHtml('<div>Test & "quotes"</div>')).toBe('&lt;div&gt;Test &amp; "quotes"&lt;/div&gt;');
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

  it('treats small numbers as milliseconds', () => {
    expect(formatDuration(150)).toBe('150ms');
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

describe('getFileIcon', () => {
  it('returns default icon for falsy values', () => {
    expect(getFileIcon('')).toBe('📄');
  });

  it('returns correct icons for known extensions', () => {
    expect(getFileIcon('file.js')).toBe('📜');
    expect(getFileIcon('file.py')).toBe('🐍');
    expect(getFileIcon('file.go')).toBe('🐹');
    expect(getFileIcon('file.png')).toBe('🖼️');
  });

  it('returns default icon for unknown extensions', () => {
    expect(getFileIcon('file.xyz')).toBe('📄');
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
});

describe('throttle', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('limits function execution rate', () => {
    const fn = vi.fn();
    const throttled = throttle(fn, 100);

    throttled('first');
    expect(fn).toHaveBeenCalledWith('first');

    throttled('second');
    expect(fn).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(100);
    throttled('third');
    expect(fn).toHaveBeenCalledTimes(2);
    expect(fn).toHaveBeenLastCalledWith('third');
  });
});

describe('deepClone', () => {
  it('clones primitive values', () => {
    expect(deepClone(42)).toBe(42);
    expect(deepClone('test')).toBe('test');
    expect(deepClone(null)).toBe(null);
  });

  it('clones dates', () => {
    const date = new Date('2023-01-01');
    const cloned = deepClone(date);
    expect(cloned).toEqual(date);
    expect(cloned).not.toBe(date);
  });

  it('clones arrays', () => {
    const arr = [1, 2, { a: 3 }];
    const cloned = deepClone(arr);
    expect(cloned).toEqual(arr);
    expect(cloned).not.toBe(arr);
    expect(cloned[2]).not.toBe(arr[2]);
  });

  it('clones objects', () => {
    const obj = { a: 1, b: { c: 2 } };
    const cloned = deepClone(obj);
    expect(cloned).toEqual(obj);
    expect(cloned).not.toBe(obj);
    expect(cloned.b).not.toBe(obj.b);
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

describe('highlightSearchTerm', () => {
  it('returns escaped text when no search term', () => {
    expect(highlightSearchTerm('<div>test</div>', '')).toBe('&lt;div&gt;test&lt;/div&gt;');
  });

  it('highlights search terms', () => {
    const result = highlightSearchTerm('hello world', 'world');
    expect(result).toContain('<mark class="bg-yellow-200 text-black">world</mark>');
  });

  it('highlights case-insensitive', () => {
    const result = highlightSearchTerm('Hello World', 'hello');
    expect(result).toContain('<mark class="bg-yellow-200 text-black">Hello</mark>');
  });

  it('escapes HTML before highlighting', () => {
    const result = highlightSearchTerm('<div>test</div>', 'test');
    expect(result).toContain('&lt;div&gt;<mark class="bg-yellow-200 text-black">test</mark>&lt;/div&gt;');
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

describe('isImageFile', () => {
  it('returns true for image extensions', () => {
    expect(isImageFile('photo.png')).toBe(true);
    expect(isImageFile('image.jpg')).toBe(true);
    expect(isImageFile('picture.JPEG')).toBe(true);
    expect(isImageFile('icon.gif')).toBe(true);
  });

  it('returns false for non-image extensions', () => {
    expect(isImageFile('document.pdf')).toBe(false);
    expect(isImageFile('script.js')).toBe(false);
    expect(isImageFile('data.json')).toBe(false);
  });
});

describe('formatTimestamp', () => {
  it('returns empty string for falsy values', () => {
    expect(formatTimestamp(null)).toBe('');
    expect(formatTimestamp(undefined)).toBe('');
    expect(formatTimestamp('')).toBe('');
  });

  it('formats timestamp to locale string', () => {
    const timestamp = '2023-01-01T12:00:00Z';
    const result = formatTimestamp(timestamp);
    expect(result).toMatch(/0?1\/0?1\/2023/);
  });
});