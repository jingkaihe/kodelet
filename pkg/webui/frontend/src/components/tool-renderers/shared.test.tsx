import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import * as utils from '../../utils';
import { CopyButton, ExternalLink, formatJsonObjectOrArray, safeStringify } from './shared';

// Mock utils
vi.mock('../../utils', async () => {
  const actual = await vi.importActual('../../utils');
  return {
    ...actual,
    copyToClipboard: vi.fn(),
    escapeUrl: vi.fn((url) => url),
  };
});

describe('CopyButton', () => {
  it('renders copy button', () => {
    render(<CopyButton content="Copy me" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    expect(button).toBeInTheDocument();
    expect(button).toHaveClass('panel-action-button');
    expect(button).toHaveAttribute('type', 'button');
  });

  it('calls copyToClipboard when clicked', () => {
    render(<CopyButton content="Copy this text" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    fireEvent.click(button);

    expect(utils.copyToClipboard).toHaveBeenCalledWith('Copy this text');
  });

  it('applies custom className', () => {
    render(<CopyButton content="Copy me" className="custom-copy" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    expect(button).toHaveClass('panel-action-button', 'custom-copy');
  });
});

describe('ExternalLink', () => {
  it('renders link with children', () => {
    render(<ExternalLink href="https://example.com">Example Link</ExternalLink>);

    const link = screen.getByRole('link', { name: 'Open in new tab' });
    expect(link).toHaveAttribute('href', 'https://example.com');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(screen.getByText('Example Link')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(
      <ExternalLink href="https://example.com" className="custom-link">
        Link
      </ExternalLink>
    );

    const link = screen.getByRole('link');
    expect(link).toHaveClass('tool-action-link', 'custom-link');
  });

  it('handles invalid URL', () => {
    vi.mocked(utils.escapeUrl).mockReturnValueOnce('#');

    render(<ExternalLink href="javascript:alert(1)">Bad Link</ExternalLink>);

    expect(screen.getByText('Invalid URL')).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('uses escaped URL', () => {
    vi.mocked(utils.escapeUrl).mockReturnValueOnce('https://safe-url.com');

    render(<ExternalLink href="https://example.com">Link</ExternalLink>);

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://safe-url.com');
  });
});

describe('safeStringify', () => {
  it('pretty-prints objects and handles circular references', () => {
    const value: Record<string, unknown> = { key: 'value' };
    value.self = value;

    expect(safeStringify(value)).toContain('[Circular]');
    expect(safeStringify({ key: 'value' })).toBe('{\n  "key": "value"\n}');
  });
});

describe('formatJsonObjectOrArray', () => {
  it('formats valid JSON objects and arrays', () => {
    expect(formatJsonObjectOrArray('{"key":"value"}')?.formatted).toBe('{\n  "key": "value"\n}');
    expect(formatJsonObjectOrArray('[1,2]')?.formatted).toBe('[\n  1,\n  2\n]');
  });

  it('does not treat primitives or invalid JSON as formattable JSON output', () => {
    expect(formatJsonObjectOrArray('"hello"')).toBeNull();
    expect(formatJsonObjectOrArray('42')).toBeNull();
    expect(formatJsonObjectOrArray('not json')).toBeNull();
  });
});
