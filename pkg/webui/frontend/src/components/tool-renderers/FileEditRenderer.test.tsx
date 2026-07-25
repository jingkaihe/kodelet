import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileEditRenderer from './FileEditRenderer';
import { ToolResult } from '../../types';

describe('FileEditRenderer', () => {
  const createToolResult = (
    metadata: Record<string, unknown> | null | undefined,
    overrides: Partial<ToolResult> = {}
  ): ToolResult => ({
    toolName: 'file_edit',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
    ...overrides,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileEditRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders with the same operation, counts, and absolute line gutters as apply_patch', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      unifiedDiff:
        '--- /src/main.js\n+++ /src/main.js\n@@ -10 +10 @@\n-const x = 1;\n+const x = 2;\n',
      edits: [
        { startLine: 10, endLine: 10, oldContent: 'const x = 1;', newContent: 'const x = 2;' },
      ],
    });

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.getByText('/src/main.js')).toBeInTheDocument();
    expect(screen.getByText('+1')).toBeInTheDocument();
    expect(screen.getByText('-1')).toBeInTheDocument();
    expect(screen.queryByText('1 replacement')).not.toBeInTheDocument();
    expect(screen.queryByText('targeted edit')).not.toBeInTheDocument();

    const addedLine = container.querySelector('.diff-line-added');
    expect(addedLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('');
    expect(addedLine?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('10');

    const removedLine = container.querySelector('.diff-line-removed');
    expect(removedLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('10');
    expect(removedLine?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('');
  });

  it('combines replace-all hunks under one file header', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
      replaceAll: true,
      replacedCount: 2,
      unifiedDiff: '@@ -5 +5,2 @@\n-old\n+new\n+extra\n@@ -10 +11 @@\n-old2\n+new2\n',
      edits: [
        { startLine: 5, endLine: 5, oldContent: 'old', newContent: 'new\nextra' },
        { startLine: 10, endLine: 10, oldContent: 'old2', newContent: 'new2' },
      ],
    });

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('+3')).toBeInTheDocument();
    expect(screen.getByText('-2')).toBeInTheDocument();
    expect(screen.getByText('@@ -5 +5,2 @@')).toBeInTheDocument();
    expect(screen.getByText('@@ -10 +11 @@')).toBeInTheDocument();

    const addedLines = container.querySelectorAll('.diff-line-added');
    expect(addedLines[2]?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('11');
  });

  it('renders without diff blocks when there are no edits', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      edits: [],
    });

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(screen.getByText('/empty.txt')).toBeInTheDocument();
    expect(screen.getByText('+0')).toBeInTheDocument();
    expect(screen.getByText('-0')).toBeInTheDocument();
    expect(container.querySelector('.diff-block')).not.toBeInTheDocument();
  });

  it('renders partial diff content and error details on failure', () => {
    const toolResult = createToolResult(
      {
        filePath: '/src/main.js',
        unifiedDiff: '@@ -10 +10 @@\n-old\n+new\n',
        edits: [{ startLine: 10, endLine: 10, oldContent: 'old', newContent: 'new' }],
      },
      { success: false, error: 'failed to write file' }
    );

    const { container } = render(<FileEditRenderer toolResult={toolResult} />);

    expect(container.firstChild).toHaveClass('apply-patch-result-failed');
    expect(screen.getByText('failed to write file')).toBeInTheDocument();
    expect(screen.getByText('/src/main.js')).toBeInTheDocument();
    expect(screen.getByText('old')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
  });
});
