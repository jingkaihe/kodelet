import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

  it('renders summary counts and file paths', () => {
    const toolResult = createToolResult({
      added: ['/tmp/new.txt'],
      modified: ['/tmp/edit.txt'],
      deleted: ['/tmp/old.txt'],
      changes: [],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);

    expect(screen.getByText('3 files')).toBeInTheDocument();
    expect(screen.getByText('A 1')).toBeInTheDocument();
    expect(screen.getByText('M 1')).toBeInTheDocument();
    expect(screen.getByText('D 1')).toBeInTheDocument();
    expect(screen.getByText('/tmp/new.txt')).toBeInTheDocument();
    expect(screen.getByText('/tmp/edit.txt')).toBeInTheDocument();
    expect(screen.getByText('/tmp/old.txt')).toBeInTheDocument();
  });

  it('reveals unified diff details when expanded', () => {
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

    render(<ApplyPatchRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('Show diffs (1)'));

    expect(screen.getAllByText('/tmp/edit.txt')).toHaveLength(2);
    expect(screen.getByText('Updated')).toBeInTheDocument();
    expect(screen.getByText('@@ -1,2 +1,2 @@')).toBeInTheDocument();
    expect(screen.getByText('-old')).toBeInTheDocument();
    expect(screen.getByText('+new')).toBeInTheDocument();
    expect(screen.getByText('context')).toBeInTheDocument();
  });

  it('renders fallback diff for add change without unified diff', () => {
    const toolResult = createToolResult({
      changes: [
        {
          path: '/tmp/new.txt',
          operation: 'add',
          newContent: 'line1\nline2\n',
        },
      ],
    });

    render(<ApplyPatchRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('Show diffs (1)'));

    expect(screen.getByText('Added')).toBeInTheDocument();
    expect(screen.getByText('+line1')).toBeInTheDocument();
    expect(screen.getByText('+line2')).toBeInTheDocument();
  });
});
