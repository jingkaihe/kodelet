import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ApplyPatchRenderer from './ApplyPatchRenderer';
import { ToolResult } from '../../types';

const createToolResult = (
  metadata: Record<string, unknown> | null | undefined
): ToolResult => ({
  toolName: 'apply_patch',
  success: true,
  timestamp: '2023-01-01T00:00:00Z',
  metadata: metadata as Record<string, unknown> | undefined,
});

describe('ApplyPatchRenderer', () => {
  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders summary badges and changed file list', () => {
    const toolResult = createToolResult({
      added: ['/tmp/new.txt'],
      modified: ['/tmp/edit.txt'],
      deleted: ['/tmp/old.txt'],
      changes: [],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('0 changes')).toBeInTheDocument();
    expect(screen.getByText('A 1')).toBeInTheDocument();
    expect(screen.getByText('M 1')).toBeInTheDocument();
    expect(screen.getByText('D 1')).toBeInTheDocument();
    expect(screen.getByText('A /tmp/new.txt')).toBeInTheDocument();
    expect(screen.getByText('M /tmp/edit.txt')).toBeInTheDocument();
    expect(screen.getByText('D /tmp/old.txt')).toBeInTheDocument();
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

    expect(screen.getByText('Change: Update')).toBeInTheDocument();
    expect(screen.getByText('/tmp/edit.txt')).toBeInTheDocument();
    expect(screen.getByText('@@ -1,2 +1,2 @@')).toBeInTheDocument();
    expect(screen.getByText('old')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
    expect(screen.getByText('context')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-added')).toBeInTheDocument();
    expect(container.querySelector('.diff-line-removed')).toBeInTheDocument();
  });

  it('renders fallback diffs when unified diff is unavailable', () => {
    const toolResult = createToolResult({
      changes: [
        {
          path: '/tmp/new.txt',
          operation: 'add',
          newContent: 'line1\nline2\n',
        },
      ],
    });

    const { container } = render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('Change: Add')).toBeInTheDocument();
    expect(screen.getByText('/tmp/new.txt')).toBeInTheDocument();
    expect(screen.getByText('line1')).toBeInTheDocument();
    expect(screen.getByText('line2')).toBeInTheDocument();
    expect(container.querySelectorAll('.diff-line-added')).toHaveLength(2);
  });

  it('renders a focused preview for large added files', () => {
    const lines = Array.from({ length: 20 }, (_, index) => `line${index + 1}`).join('\n');
    const toolResult = createToolResult({
      added: ['/tmp/large.txt'],
      changes: [
        {
          path: '/tmp/large.txt',
          operation: 'add',
          newContent: lines,
        },
      ],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('line1')).toBeInTheDocument();
    expect(screen.getByText('line20')).toBeInTheDocument();
    expect(screen.getByText('... 6 more diff lines omitted ...')).toBeInTheDocument();
    expect(screen.queryByText('line10')).not.toBeInTheDocument();
  });
});
