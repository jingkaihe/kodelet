import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ApplyPatchRenderer from './ApplyPatchRenderer';
import { ToolResult } from '../../types';

const createToolResult = (
  metadata: Record<string, unknown> | null | undefined,
  overrides: Partial<ToolResult> = {}
): ToolResult => ({
  toolName: 'apply_patch',
  success: true,
  timestamp: '2023-01-01T00:00:00Z',
  metadata: metadata as Record<string, unknown> | undefined,
  ...overrides,
});

describe('ApplyPatchRenderer', () => {
  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders structured changes with operations, counts, and line gutters', () => {
    const toolResult = createToolResult({
      changes: [
        {
          path: '/tmp/new.txt',
          operation: 'add',
          unifiedDiff: '--- /dev/null\n+++ /tmp/new.txt\n@@ -0,0 +1,1 @@\n+hello\n',
        },
        {
          path: '/tmp/edit.txt',
          operation: 'update',
          unifiedDiff: '--- /tmp/edit.txt\n+++ /tmp/edit.txt\n@@ -1,2 +1,2 @@\n-old\n+new\n context\n',
        },
        {
          path: '/tmp/old.txt',
          operation: 'delete',
          unifiedDiff: '--- /tmp/old.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-bye\n',
        },
        {
          path: '/tmp/old-name.txt',
          movePath: '/tmp/new-name.txt',
          operation: 'update',
          unifiedDiff: '--- /tmp/old-name.txt\n+++ /tmp/new-name.txt\n@@ -1 +1 @@\n-old name\n+new name\n',
        },
      ],
    });

    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.queryByText('0 changes')).not.toBeInTheDocument();
    expect(screen.queryByText('A 1')).not.toBeInTheDocument();
    expect(screen.queryByText('M 1')).not.toBeInTheDocument();
    expect(screen.queryByText('D 1')).not.toBeInTheDocument();
    expect(screen.getByText('/tmp/new.txt')).toBeInTheDocument();
    expect(screen.getByText('/tmp/edit.txt')).toBeInTheDocument();
    expect(screen.getByText('/tmp/old.txt')).toBeInTheDocument();
    expect(screen.getByText('/tmp/old-name.txt → /tmp/new-name.txt')).toBeInTheDocument();
    expect(screen.getByText('Write')).toBeInTheDocument();
    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.getByText('Delete')).toBeInTheDocument();
    expect(screen.getByText('Move')).toBeInTheDocument();

    expect(Array.from(container.querySelectorAll('.apply-patch-count-added')).map((element) => element.textContent)).toEqual([
      '+1',
      '+1',
      '+0',
      '+1',
    ]);
    expect(Array.from(container.querySelectorAll('.apply-patch-count-removed')).map((element) => element.textContent)).toEqual([
      '-0',
      '-1',
      '-1',
      '-1',
    ]);

    const firstAddedLine = container.querySelector('.diff-line-added');
    expect(firstAddedLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('');
    expect(firstAddedLine?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('1');

    const firstRemovedLine = container.querySelector('.diff-line-removed');
    expect(firstRemovedLine?.querySelectorAll('.diff-line-number')[0]).toHaveTextContent('1');
    expect(firstRemovedLine?.querySelectorAll('.diff-line-number')[1]).toHaveTextContent('');
  });

  it('renders unified diffs inline', () => {
    const toolResult = createToolResult({
      added: [],
      modified: ['/tmp/edit.txt'],
      deleted: [],
      changes: [
        {
          path: '/tmp/edit.txt',
          operation: 'update',
          unifiedDiff: '@@ -1,2 +1,2 @@\n-old\n+new\n context',
        },
      ],
    });

    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.queryByText('Change: Update')).not.toBeInTheDocument();
    expect(screen.getByText('/tmp/edit.txt')).toBeInTheDocument();
    expect(screen.getByText('@@ -1,2 +1,2 @@')).toBeInTheDocument();
    expect(screen.getByText('old')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
    expect(screen.getByText('context')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-added')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-removed')).toBeInTheDocument();
  });

  it('preserves diff content lines that look like file headers', () => {
    const toolResult = createToolResult({
      modified: ['/tmp/edit.txt'],
      changes: [
        {
          path: '/tmp/edit.txt',
          operation: 'update',
          unifiedDiff:
            '--- /tmp/edit.txt\n+++ /tmp/edit.txt\n@@ -1,2 +1,2 @@\n--- removed-literal\n+++ added-literal\n context',
        },
      ],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.queryByText('--- /tmp/edit.txt')).not.toBeInTheDocument();
    expect(screen.queryByText('+++ /tmp/edit.txt')).not.toBeInTheDocument();
    expect(screen.getByText('-- removed-literal')).toBeInTheDocument();
    expect(screen.getByText('++ added-literal')).toBeInTheDocument();
  });

  it('renders large added files without truncating the diff', () => {
    const lines = Array.from({ length: 20 }, (_, index) => `line${index + 1}`);
    const toolResult = createToolResult({
      changes: [
        {
          path: '/tmp/large.txt',
          operation: 'add',
          unifiedDiff: ['--- /dev/null', '+++ /tmp/large.txt', '@@ -0,0 +1,20 @@', ...lines.map((line) => `+${line}`)].join('\n'),
        },
      ],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('line1')).toBeInTheDocument();
    expect(screen.getByText('line20')).toBeInTheDocument();
    expect(screen.getByText('line10')).toBeInTheDocument();
    expect(screen.queryByText('... 6 more diff lines omitted ...')).not.toBeInTheDocument();
  });

  it('renders partial diff content and error details on failure', () => {
    const toolResult = createToolResult(
      {
        changes: [
          {
            path: '/tmp/partial.go',
            operation: 'update',
            unifiedDiff: '@@ -1 +1 @@\n-old\n+new\n',
          },
        ],
      },
      { success: false, error: 'could not apply hunk' }
    );

    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(container.firstChild).toHaveClass('apply-patch-result-failed');
    expect(screen.getByText('could not apply hunk')).toBeInTheDocument();
    expect(screen.getByText('/tmp/partial.go')).toBeInTheDocument();
    expect(screen.getByText('old')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
  });
});
