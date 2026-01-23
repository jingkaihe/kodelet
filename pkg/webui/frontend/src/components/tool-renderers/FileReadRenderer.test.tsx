import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileReadRenderer from './FileReadRenderer';
import { ToolResult, FileMetadata } from '../../types';

// Mock shared components
interface MockCopyButtonProps {
  content: string;
}

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  CopyButton: ({ content }: MockCopyButtonProps) => (
    <button data-testid="copy-button" data-content={content}>Copy</button>
  ),
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
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

  it('renders file path', () => {
    const toolResult = createToolResult({
      filePath: '/home/user/test.js',
      lines: ['const x = 1;', 'const y = 2;'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('/home/user/test.js')).toBeInTheDocument();
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
  });

  it('shows warning variant when file is truncated', () => {
    const toolResult = createToolResult({
      filePath: '/large-file.txt',
      lines: ['line 1', 'line 2'],
      truncated: true,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    const badge = screen.getAllByTestId('status-badge')[0];
    expect(badge).toHaveAttribute('data-variant', 'warning');
  });

  it('shows line count badge', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2', 'line 3'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('3 lines')).toBeInTheDocument();
  });

  it('detects language from file path', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      lines: ['const x = 1;'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('javascript')).toBeInTheDocument();
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

    expect(screen.getByText('/empty.txt')).toBeInTheDocument();
    expect(screen.getByText('0 lines')).toBeInTheDocument();
  });

  it('shows remaining lines info when present', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2'],
      remainingLines: 50,
      truncated: true,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('50 more')).toBeInTheDocument();
  });

  it('shows continuation tip when there are remaining lines', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2'],
      offset: 10,
      lineLimit: 2,
      remainingLines: 25,
      truncated: true,
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('Use offset=12 to continue reading')).toBeInTheDocument();
  });

  it('applies correct styling to code display', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['test content'],
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    const codeContainer = container.querySelector('.bg-kodelet-light');
    expect(codeContainer).toHaveClass('font-mono', 'rounded');
    expect(codeContainer).toHaveStyle({ maxHeight: '400px', overflowY: 'auto' });
  });
});
