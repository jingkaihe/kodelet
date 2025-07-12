import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileReadRenderer from './FileReadRenderer';
import { ToolResult, FileMetadata } from '../../types';

// Mock shared components
interface MockToolCardProps {
  title: string;
  badge?: { text: string; className: string };
  actions?: React.ReactNode;
  children: React.ReactNode;
}

interface MockCopyButtonProps {
  content: string;
}

interface MockMetadataRowProps {
  label: string;
  value: string | number;
}

vi.mock('./shared', () => ({
  ToolCard: ({ title, badge, actions, children }: MockToolCardProps) => (
    <div data-testid="tool-card">
      <h3>{title}</h3>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {actions && <div data-testid="actions">{actions}</div>}
      {children}
    </div>
  ),
  CopyButton: ({ content }: MockCopyButtonProps) => (
    <button data-testid="copy-button" data-content={content}>Copy</button>
  ),
  MetadataRow: ({ label, value }: MockMetadataRowProps) => (
    <div data-testid="metadata-row">
      {label}: {value}
    </div>
  ),
}));

// Mock utils
vi.mock('./utils', () => ({
  detectLanguageFromPath: vi.fn((path: string) => {
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.py')) return 'python';
    if (path.endsWith('.go')) return 'go';
    return null;
  }),
}));

describe('FileReadRenderer', () => {
  const createToolResult = (metadata: Partial<FileMetadata>): ToolResult => ({
    toolName: 'file_read',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as FileMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<FileReadRenderer toolResult={{ ...toolResult, metadata: undefined }} />);
    
    expect(container.firstChild).toBeNull();
  });

  it('renders file read with basic information', () => {
    const toolResult = createToolResult({
      filePath: '/home/user/test.js',
      lines: ['const x = 1;', 'const y = 2;'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('📄 File Read')).toBeInTheDocument();
    expect(screen.getByText('Path: /home/user/test.js')).toBeInTheDocument();
  });

  it('shows copy button with file content', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2', 'line 3'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', 'line 1\nline 2\nline 3');
  });

  it('displays line numbers correctly', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['first', 'second', 'third'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText(/^\s*1\s*$/)).toBeInTheDocument();
    expect(screen.getByText(/^\s*2\s*$/)).toBeInTheDocument();
    expect(screen.getByText(/^\s*3\s*$/)).toBeInTheDocument();
  });

  it('handles offset for line numbers', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['middle', 'content'],
      offset: 50,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText(/^\s*50\s*$/)).toBeInTheDocument();
    expect(screen.getByText(/^\s*51\s*$/)).toBeInTheDocument();
    expect(screen.getByText('Starting at line: 50')).toBeInTheDocument();
  });

  it('shows truncated badge when file is truncated', () => {
    const toolResult = createToolResult({
      filePath: '/large-file.txt',
      lines: ['line 1', 'line 2'],
      truncated: true,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Truncated')).toBeInTheDocument();
    const badge = screen.getByText('Truncated');
    expect(badge.className).toContain('badge-warning');
  });

  it('detects language from file path when not provided', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      lines: ['const x = 1;'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Language: javascript')).toBeInTheDocument();
  });

  it('uses provided language over detected language', () => {
    const toolResult = createToolResult({
      filePath: '/src/config.json',
      lines: ['{"key": "value"}'],
      language: 'json',
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Language: json')).toBeInTheDocument();
  });

  it('shows total lines when available', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2'],
      totalLines: 100,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Lines shown: 2 of 100')).toBeInTheDocument();
  });

  it('removes trailing empty lines', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['content', 'more content', '', '', ''],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', 'content\nmore content');
  });

  it('preserves empty lines in the middle', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', '', 'line 3'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', 'line 1\n\nline 3');
  });

  it('handles empty file', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      lines: [],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Path: /empty.txt')).toBeInTheDocument();
    expect(screen.getByText('Lines shown: 0')).toBeInTheDocument();
  });

  it('handles file with only empty lines', () => {
    const toolResult = createToolResult({
      filePath: '/whitespace.txt',
      lines: ['', '', ''],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', '');
  });

  it('adjusts line number width for large files', () => {
    const toolResult = createToolResult({
      filePath: '/large.txt',
      lines: ['content'],
      offset: 9999,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    // Should display line 9999 with proper padding
    expect(screen.getByText(/^\s*9999\s*$/)).toBeInTheDocument();
  });

  it('renders empty lines as non-breaking spaces', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', '', 'line 3'],
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    const codeLines = container.querySelectorAll('.flex-grow.overflow-x-auto.whitespace-pre > div');
    expect(codeLines).toHaveLength(3);
    expect(codeLines[1].textContent).toBe('\u00A0'); // Non-breaking space
  });

  it('applies correct styling to code display', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['test content'],
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    const codeContainer = container.querySelector('.bg-base-300');
    expect(codeContainer).toHaveClass('text-sm', 'font-mono', 'rounded-lg');
    expect(codeContainer).toHaveStyle({ maxHeight: '600px', overflowY: 'auto' });
  });
});