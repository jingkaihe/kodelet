import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import GitDiffModal from './GitDiffModal';

const { copyToClipboardMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn(),
}));

vi.mock('../../utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../utils')>();
  return {
    ...actual,
    copyToClipboard: copyToClipboardMock,
  };
});

describe('GitDiffModal', () => {
  beforeEach(() => {
    copyToClipboardMock.mockReset();
  });

  it('renders the full diff without omission markers', () => {
    const diff = [
      'diff --git a/file.txt b/file.txt',
      'index 1111111..2222222 100644',
      '--- a/file.txt',
      '+++ b/file.txt',
      '@@ -1,260 +1,260 @@',
      ...Array.from({ length: 260 }, (_, index) => ` line-${index + 1}`),
    ].join('\n');

    render(
      <GitDiffModal
        cwdLabel="/tmp/project"
        error={null}
        gitDiff={{
          cwd: '/tmp/project',
          diff,
          exit_code: 0,
          git_root: '/tmp/project',
          has_diff: true,
        }}
        loading={false}
        onClose={vi.fn()}
        onRefresh={vi.fn()}
        open
      />
    );

    expect(screen.getByText('line-260')).toBeInTheDocument();
    expect(screen.queryByText(/more diff lines omitted/i)).not.toBeInTheDocument();
  });

  it('renders a simplified header and hides repo metadata', () => {
    render(
      <GitDiffModal
        cwdLabel="/tmp/project"
        error={null}
        gitDiff={{
          cwd: '/tmp/project',
          diff: 'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new',
          exit_code: 0,
          git_root: '/tmp/project',
          has_diff: true,
        }}
        loading={false}
        onClose={vi.fn()}
        onRefresh={vi.fn()}
        open
      />
    );

    expect(screen.getByRole('heading', { name: 'Git diff' })).toBeInTheDocument();
    expect(screen.getByText('/tmp/project')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy diff' })).toBeInTheDocument();
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument();
    expect(screen.queryByText(/Uncommitted changes|Working tree clean/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Repo /)).not.toBeInTheDocument();
    expect(screen.queryByText(/git diff --no-ext-diff/)).not.toBeInTheDocument();
  });

  it('copies the full diff from the header action', () => {
    const diff = 'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new';

    render(
      <GitDiffModal
        cwdLabel="/tmp/project"
        error={null}
        gitDiff={{
          cwd: '/tmp/project',
          diff,
          exit_code: 0,
          git_root: '/tmp/project',
          has_diff: true,
        }}
        loading={false}
        onClose={vi.fn()}
        onRefresh={vi.fn()}
        open
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy diff' }));

    expect(copyToClipboardMock).toHaveBeenCalledWith(diff);
  });
});
