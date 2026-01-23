import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import FileWriteRenderer from './FileWriteRenderer';
import { ToolResult } from '../../types';

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

vi.mock('./utils', () => ({
  detectLanguageFromPath: vi.fn((path: string) => {
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.py')) return 'python';
    return null;
  }),
  formatFileSize: vi.fn((size: number) => {
    if (size < 1024) return `${size} B`;
    return `${(size / 1024).toFixed(1)} KB`;
  }),
}));

describe('FileWriteRenderer', () => {
  const createToolResult = (metadata: Record<string, unknown> | null | undefined): ToolResult => ({
    toolName: 'file_write',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders file path', () => {
    const toolResult = createToolResult({
      filePath: '/home/user/output.txt',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.getByText('/home/user/output.txt')).toBeInTheDocument();
  });

  it('shows Written badge', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.getByText('Written')).toBeInTheDocument();
  });

  it('shows file size when available', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      size: 1536,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.getByText('1.5 KB')).toBeInTheDocument();
  });

  it('detects language from file path', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.getByText('javascript')).toBeInTheDocument();
  });

  it('shows copy button when content is available', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      content: 'Hello, World!',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', 'Hello, World!');
  });

  it('does not show copy button when content is missing', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
  });

  it('shows "Show content" button when content is available', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      content: 'const x = 1;\nconst y = 2;',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.getByText('Show content (2 lines)')).toBeInTheDocument();
  });

  it('reveals content when button is clicked', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      content: 'const x = 1;',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('Show content (1 lines)'));
    expect(screen.getByText('const x = 1;')).toBeInTheDocument();
  });

  it('handles empty content', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      content: '',
      size: 0,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);
    expect(screen.queryByText(/Show content/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
  });
});
