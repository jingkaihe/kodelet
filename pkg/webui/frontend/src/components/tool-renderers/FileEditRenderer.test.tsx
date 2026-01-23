import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import FileEditRenderer from './FileEditRenderer';
import { ToolResult } from '../../types';

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

describe('FileEditRenderer', () => {
  const createToolResult = (metadata: Record<string, unknown> | null | undefined): ToolResult => ({
    toolName: 'file_edit',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileEditRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders file path', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
      edits: [],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    expect(screen.getByText('/src/app.js')).toBeInTheDocument();
  });

  it('shows edit count badge', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      edits: [
        { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
        { startLine: 5, endLine: 6, oldContent: 'foo', newContent: 'bar' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    expect(screen.getByText('2 edits')).toBeInTheDocument();
  });

  it('shows singular edit for single edit', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      edits: [
        { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    expect(screen.getByText('1 edit')).toBeInTheDocument();
  });

  it('shows "Show diff" button', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      edits: [
        { startLine: 10, endLine: 12, oldContent: 'const x = 1;', newContent: 'const x = 2;' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    expect(screen.getByText('Show diff')).toBeInTheDocument();
  });

  it('reveals diff when button is clicked', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      edits: [
        { startLine: 10, endLine: 12, oldContent: 'const x = 1;', newContent: 'const x = 2;' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('Show diff'));
    expect(screen.getByText('Lines 10-12')).toBeInTheDocument();
  });

  it('renders diff with added and removed lines', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      edits: [
        { startLine: 1, endLine: 1, oldContent: 'old line', newContent: 'new line' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('Show diff'));

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getAllByText('Show diff')[0]);
    expect(container.querySelector('.bg-red-50')).toBeInTheDocument();
    expect(container.querySelector('.bg-green-50')).toBeInTheDocument();
  });

  it('handles empty edits array', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      edits: [],
    });

    render(<FileEditRenderer toolResult={toolResult} />);
    expect(screen.queryByText('Show diff')).not.toBeInTheDocument();
  });

  describe('Replace All', () => {
    it('shows replacement count for replace all', () => {
      const toolResult = createToolResult({
        filePath: '/src/app.js',
        replaceAll: true,
        replacedCount: 3,
        edits: [
          { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
          { startLine: 5, endLine: 5, oldContent: 'old', newContent: 'new' },
          { startLine: 10, endLine: 10, oldContent: 'old', newContent: 'new' },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);
      expect(screen.getByText('3 replacements')).toBeInTheDocument();
      expect(screen.getByText('(replace all)')).toBeInTheDocument();
    });

    it('shows singular replacement for single replace all', () => {
      const toolResult = createToolResult({
        filePath: '/src/app.js',
        replaceAll: true,
        replacedCount: 1,
        edits: [
          { startLine: 5, endLine: 7, oldContent: 'old code', newContent: 'new code' },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);
      expect(screen.getByText('1 replacement')).toBeInTheDocument();
    });
  });
});
