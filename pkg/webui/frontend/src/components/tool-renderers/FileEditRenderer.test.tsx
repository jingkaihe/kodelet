import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileEditRenderer from './FileEditRenderer';
import { ToolResult } from '../../types';

describe('FileEditRenderer', () => {
  const createToolResult = (
    metadata: Record<string, unknown> | null | undefined
  ): ToolResult => ({
    toolName: 'file_edit',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileEditRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders targeted edit metadata and diff blocks', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      edits: [
        { startLine: 10, endLine: 12, oldContent: 'const x = 1;', newContent: 'const x = 2;' },
      ],
    });

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('/src/main.js')).toBeInTheDocument();
    expect(screen.getByText('1 replacement')).toBeInTheDocument();
    expect(screen.getByText('targeted edit')).toBeInTheDocument();
    expect(screen.getByText('Lines 10-12')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-added')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-removed')).toBeInTheDocument();
  });

  it('renders replace-all metadata with replacement counts', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
      replaceAll: true,
      replacedCount: 3,
      edits: [
        { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
      ],
    });

    render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('3 replacements')).toBeInTheDocument();
    expect(screen.getByText('replace all')).toBeInTheDocument();
  });

  it('renders without diff blocks when there are no edits', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      edits: [],
    });

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('/empty.txt')).toBeInTheDocument();
    expect(container.querySelector('.diff-block')).not.toBeInTheDocument();
  });
});
