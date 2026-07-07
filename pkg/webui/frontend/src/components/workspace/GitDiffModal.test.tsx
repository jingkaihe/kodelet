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

    expect(screen.queryByRole('heading', { name: 'Changes' })).not.toBeInTheDocument();
    expect(screen.queryByText('/tmp/project')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy diff' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Refresh diff' })).toBeInTheDocument();
    expect(screen.getByTestId('git-diff-panel')).toHaveAttribute('role', 'complementary');
    expect(screen.queryByTestId('git-diff-modal-backdrop')).not.toBeInTheDocument();
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument();
    expect(screen.queryByText(/Uncommitted changes|Working tree clean/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Repo /)).not.toBeInTheDocument();
    expect(screen.queryByText(/git diff --no-ext-diff/)).not.toBeInTheDocument();
  });

  it('does not classify subsequent file headers as changed lines', () => {
    const diff = [
      'diff --git a/one.txt b/one.txt',
      'index 1111111..2222222 100644',
      '--- a/one.txt',
      '+++ b/one.txt',
      '@@ -1 +1 @@',
      '-old one',
      '+new one',
      'diff --git a/two.txt b/two.txt',
      'index 3333333..4444444 100644',
      '--- a/two.txt',
      '+++ b/two.txt',
      '@@ -10 +20 @@',
      '-old two',
      '+new two',
    ].join('\n');

    const { container } = render(
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

    const addedContents = Array.from(container.querySelectorAll('.diff-line-added .diff-content'))
      .map((element) => element.textContent);
    const removedContents = Array.from(container.querySelectorAll('.diff-line-removed .diff-content'))
      .map((element) => element.textContent);

    expect(addedContents).toEqual(['new one', 'new two']);
    expect(removedContents).toEqual(['old one', 'old two']);
  });

  it('copies the full diff from the floating icon action', () => {
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
