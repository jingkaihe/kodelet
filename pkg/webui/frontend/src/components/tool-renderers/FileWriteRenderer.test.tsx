import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileWriteRenderer from './FileWriteRenderer';
import { ToolResult } from '../../types';

describe('FileWriteRenderer', () => {
  const createToolResult = (
    metadata: Record<string, unknown> | null | undefined,
    overrides: Partial<ToolResult> = {}
  ): ToolResult => ({
    toolName: 'file_write',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
    ...overrides,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders new-file additions with the shared Write header, counts, and line gutters', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
      size: 1536,
      language: 'javascript',
      unifiedDiff: '--- /dev/null\n+++ /src/app.js\n@@ -0,0 +1,2 @@\n+const x = 1;\n+const y = 2;\n',
    });

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('/src/app.js')).toBeInTheDocument();
    expect(screen.getByText('Write')).toHaveClass('apply-patch-operation-write');
    expect(screen.getByText('+2')).toBeInTheDocument();
    expect(screen.getByText('-0')).toBeInTheDocument();
    expect(screen.queryByText('written')).not.toBeInTheDocument();
    expect(screen.queryByText('javascript')).not.toBeInTheDocument();
    expect(screen.queryByText('1.5 KB')).not.toBeInTheDocument();
    expect(container.querySelector('.diff-block')).toBeInTheDocument();
    const addedLines = container.querySelectorAll('.diff-line-added');
    expect(addedLines).toHaveLength(2);
    expect(addedLines[0].querySelectorAll('.diff-line-number')[0]).toBeEmptyDOMElement();
    expect(addedLines[0].querySelectorAll('.diff-line-number')[1]).toHaveTextContent('1');
    expect(addedLines[1].querySelectorAll('.diff-line-number')[1]).toHaveTextContent('2');
  });

  it('renders overwrites with real additions, removals, and absolute line numbers', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      content: 'const x = 1;\nconst y = 2;',
      unifiedDiff: '@@ -10,2 +10,3 @@\n-const x = 0;\n+const x = 1;\n+const y = 2;\n unchanged\n',
    });

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('Write')).toBeInTheDocument();
    expect(screen.getByText('+2')).toBeInTheDocument();
    expect(screen.getByText('-1')).toBeInTheDocument();
    expect(screen.getByText('const x = 0;')).toBeInTheDocument();
    const removedLine = container.querySelector('.diff-line-removed');
    expect(removedLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('10');
    expect(removedLine?.querySelectorAll('.diff-line-number')[1]).toBeEmptyDOMElement();
    const addedLines = container.querySelectorAll('.diff-line-added');
    expect(addedLines).toHaveLength(2);
    expect(addedLines[0].querySelectorAll('.diff-line-number')[0]).toBeEmptyDOMElement();
    expect(addedLines[0].querySelectorAll('.diff-line-number')[1]).toHaveTextContent('10');
    expect(addedLines[1].querySelectorAll('.diff-line-number')[1]).toHaveTextContent('11');
    const contextLine = screen.getByText('unchanged').closest('.diff-line');
    expect(contextLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('11');
    expect(contextLine?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('12');
    expect(container.querySelector('.tool-code-block')).not.toBeInTheDocument();
  });

  it.each(['', undefined])('renders no diff or legacy content fallback for unifiedDiff %s', (unifiedDiff) => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      content: 'unchanged content',
      unifiedDiff,
    });

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(container.querySelector('.tool-code-block')).not.toBeInTheDocument();
    expect(container.querySelector('.diff-block')).not.toBeInTheDocument();
    expect(screen.getByText('Write')).toBeInTheDocument();
    expect(screen.getByText('/test.txt')).toBeInTheDocument();
    expect(screen.getByText('+0')).toBeInTheDocument();
    expect(screen.getByText('-0')).toBeInTheDocument();
    expect(screen.queryByText('unchanged content')).not.toBeInTheDocument();
  });

  it('renders error details with the shared failure presentation', () => {
    const toolResult = createToolResult(
      { filePath: '/test.txt', unifiedDiff: '@@ -1 +1 @@\n-old\n+new\n' },
      { success: false, error: 'failed to write file' }
    );

    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);

    expect(container.firstChild).toHaveClass('apply-patch-result-failed');
    expect(screen.getByText('failed to write file')).toBeInTheDocument();
    expect(screen.getByText('/test.txt')).toBeInTheDocument();
    expect(screen.getByText('old')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
  });
});
