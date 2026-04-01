// Utility functions for Kodelet Web UI

import { format, formatDistanceToNow } from 'date-fns';
import { Usage } from '../types';

const formatCompactNumber = (value: number): string => {
  return new Intl.NumberFormat('en-US', {
    notation: value >= 1000 ? 'compact' : 'standard',
    maximumFractionDigits: value >= 1000 ? 1 : 0,
  }).format(value);
};

// Date formatting utility
export const formatDate = (dateString: string | null | undefined): string => {
  if (!dateString) return 'N/A';

  const date = new Date(dateString);
  const now = new Date();
  const diff = now.getTime() - date.getTime();

  // If less than a day, show relative time
  if (diff < 24 * 60 * 60 * 1000) {
    return formatDistanceToNow(date, { addSuffix: true });
  }

  // Otherwise show formatted date
  return format(date, 'MMM d, yyyy h:mm a');
};

export const formatCompactRelativeTime = (dateString: string | null | undefined): string => {
  if (!dateString) return '—';

  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) {
    return '—';
  }

  const diffMs = Math.max(0, Date.now() - date.getTime());
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;

  if (diffMs < minute) return 'now';
  if (diffMs < hour) return `${Math.floor(diffMs / minute)}m`;
  if (diffMs < day) return `${Math.floor(diffMs / hour)}h`;
  if (diffMs < 7 * day) return `${Math.floor(diffMs / day)}d`;

  return format(date, 'MMM d');
};

// Cost formatting utility
export const formatCost = (usage: Usage | null | undefined): string => {
  if (!usage) return '$0.00';

  const total = (usage.inputCost || 0) + (usage.outputCost || 0) +
                (usage.cacheCreationCost || 0) + (usage.cacheReadCost || 0);

  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 4
  }).format(total);
};

export const formatTokenUsage = (usage: Usage | null | undefined): string => {
  if (!usage) return '0 tokens';

  const total =
    (usage.inputTokens || 0) +
    (usage.outputTokens || 0) +
    (usage.cacheCreationInputTokens || 0) +
    (usage.cacheReadInputTokens || 0);

  return `${formatCompactNumber(total)} tokens`;
};

export const formatContextWindow = (usage: Usage | null | undefined): string | null => {
  if (!usage?.currentContextWindow || !usage?.maxContextWindow) {
    return null;
  }

  const current = usage.currentContextWindow;
  const max = usage.maxContextWindow;
  const percentage = Math.max(0, Math.min(100, Math.round((current / max) * 100)));

  return `${formatCompactNumber(current)}/${formatCompactNumber(max)} (${percentage}%) context`;
};

const fallbackCopyToClipboard = (text: string): boolean => {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') {
    return false;
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '0';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';

  const selection = document.getSelection();
  const previousRanges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange())
    : [];
  const activeElement = document.activeElement instanceof HTMLElement
    ? document.activeElement
    : null;

  document.body.appendChild(textarea);

  try {
    textarea.focus();
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    return document.execCommand('copy');
  } finally {
    textarea.remove();

    if (selection) {
      selection.removeAllRanges();
      for (const range of previousRanges) {
        selection.addRange(range);
      }
    }

    activeElement?.focus();
  }
};

// Copy to clipboard utility
export const copyToClipboard = async (text: string): Promise<void> => {
  let clipboardError: unknown;

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      showToast('Copied to clipboard!', 'success');
      return;
    }
  } catch (err) {
    clipboardError = err;
  }

  if (fallbackCopyToClipboard(text)) {
    showToast('Copied to clipboard!', 'success');
    return;
  }

  console.error('Failed to copy:', clipboardError || new Error('Clipboard API unavailable'));
  showToast('Failed to copy to clipboard', 'error');
};

// Toast notification utility
export const showToast = (
  message: string,
  type: 'info' | 'success' | 'error' | 'neutral' = 'info'
): void => {
  const toast = document.createElement('div');
  toast.className = 'toast toast-top toast-end';

  const alertClass =
    type === 'error'
      ? 'error'
      : type === 'success'
        ? 'success'
        : type === 'neutral'
          ? 'neutral'
          : 'info';

  toast.innerHTML = `
    <div class="alert alert-${alertClass}">
      <span>${escapeHtml(message)}</span>
    </div>
  `;

  document.body.appendChild(toast);

  setTimeout(() => {
    toast.remove();
  }, 3000);
};

// HTML escape utility
export const escapeHtml = (text: string): string => {
  if (!text) return '';
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
};

// URL validation utility
export const escapeUrl = (url: string): string => {
  if (!url) return '';
  try {
    const parsed = new URL(url);
    // Only allow http(s) and file protocols
    if (!['http:', 'https:', 'file:'].includes(parsed.protocol)) {
      return '#';
    }
    return url;
  } catch {
    return '#';
  }
};

// File size formatting utility
export const formatFileSize = (bytes: number): string => {
  if (!bytes) return '';
  const sizes = ['B', 'KB', 'MB', 'GB'];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < sizes.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${size.toFixed(1)} ${sizes[unit]}`;
};

// Duration formatting utility
export const formatDuration = (duration: number | string): string => {
  if (typeof duration === 'string') {
    return duration;
  }
  // If it's in nanoseconds, convert to seconds
  if (duration > 1000000000) {
    return `${(duration / 1000000000).toFixed(3)}s`;
  }
  return `${duration}ms`;
};

// Language detection from file path
export const detectLanguageFromPath = (filePath: string): string => {
  if (!filePath) return '';
  const ext = filePath.split('.').pop()?.toLowerCase();
  const langMap: Record<string, string> = {
    'js': 'javascript', 'ts': 'typescript', 'py': 'python', 'go': 'go',
    'java': 'java', 'cpp': 'cpp', 'c': 'c', 'cs': 'csharp',
    'php': 'php', 'rb': 'ruby', 'rs': 'rust', 'sh': 'bash',
    'html': 'html', 'css': 'css', 'json': 'json', 'xml': 'xml',
    'yaml': 'yaml', 'yml': 'yaml', 'md': 'markdown', 'sql': 'sql'
  };
  return langMap[ext || ''] || ext || '';
};

// File icon utility
export const getFileIcon = (path: string): string => {
  if (!path) return '📄';
  const ext = path.split('.').pop()?.toLowerCase();
  const iconMap: Record<string, string> = {
    'js': '📜', 'ts': '📜', 'py': '🐍', 'go': '🐹', 'java': '☕',
    'html': '🌐', 'css': '🎨', 'json': '📋', 'xml': '📄',
    'md': '📝', 'txt': '📄', 'log': '📊',
    'jpg': '🖼️', 'jpeg': '🖼️', 'png': '🖼️', 'gif': '🖼️',
    'pdf': '📕', 'doc': '📘', 'docx': '📘',
    'zip': '📦', 'tar': '📦', 'gz': '📦'
  };
  return iconMap[ext || ''] || '📄';
};

// Debounce utility
export const debounce = <T extends unknown[]>(
  func: (...args: T) => void,
  delay: number
): ((...args: T) => void) => {
  let timeoutId: number;
  return (...args: T) => {
    clearTimeout(timeoutId);
    timeoutId = window.setTimeout(() => func(...args), delay);
  };
};

// Throttle utility
export const throttle = <T extends unknown[]>(
  func: (...args: T) => void,
  delay: number
): ((...args: T) => void) => {
  let lastCall = 0;
  return (...args: T) => {
    const now = Date.now();
    if (now - lastCall >= delay) {
      lastCall = now;
      func(...args);
    }
  };
};

// Deep clone utility
export const deepClone = <T>(obj: T): T => {
  if (obj === null || typeof obj !== 'object') return obj;
  if (obj instanceof Date) return new Date(obj.getTime()) as T;
  if (obj instanceof Array) return obj.map(item => deepClone(item)) as T;
  if (obj instanceof Object) {
    const cloned: Record<string, unknown> = {};
    for (const key in obj) {
      if (Object.prototype.hasOwnProperty.call(obj, key)) {
        cloned[key] = deepClone((obj as Record<string, unknown>)[key]);
      }
    }
    return cloned as T;
  }
  return obj;
};

// Class name utility (similar to clsx)
export const cn = (...inputs: (string | undefined | null | boolean)[]): string => {
  return inputs.filter(Boolean).join(' ');
};

// Highlight search terms in text
export const highlightSearchTerm = (text: string, searchTerm: string): string => {
  if (!searchTerm || !text) return escapeHtml(text);

  try {
    const escaped = escapeHtml(text);
    const regex = new RegExp(`(${searchTerm.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
    return escaped.replace(regex, '<mark class="bg-yellow-200 text-black">$1</mark>');
  } catch {
    return escapeHtml(text);
  }
};

// Truncate text utility
export const truncateText = (text: string, maxLength: number): string => {
  if (text.length <= maxLength) return text;
  return text.substring(0, maxLength) + '...';
};

// Check if image file
export const isImageFile = (path: string): boolean => {
  const imageExts = ['.png', '.jpg', '.jpeg', '.gif', '.bmp', '.webp'];
  return imageExts.some(ext => path.toLowerCase().endsWith(ext));
};

// Format timestamp
export const formatTimestamp = (timestamp: string | null | undefined): string => {
  if (!timestamp) return '';
  return new Date(timestamp).toLocaleString();
};
