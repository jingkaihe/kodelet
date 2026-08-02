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

  it('keeps large file diffs collapsed until toggled and then renders every line', () => {
    const diff = [
      'diff --git a/file.txt b/file.txt',
      'new file mode 100644',
      '--- /dev/null',
      '+++ b/file.txt',
      '@@ -0,0 +1,1200 @@',
      ...Array.from({ length: 1200 }, (_, index) => `+line-${index + 1}`),
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

    const fileToggle = screen.getByRole('button', { name: 'Expand file.txt' });
    expect(fileToggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('+1200')).toBeInTheDocument();
    expect(screen.queryByText('line-1200')).not.toBeInTheDocument();

    fireEvent.click(fileToggle);

    expect(screen.getByRole('button', { name: 'Collapse file.txt' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(screen.getByText('line-1200')).toBeInTheDocument();
    expect(screen.queryByText(/more diff lines omitted/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Collapse file.txt' }));
    expect(screen.queryByText('line-1200')).not.toBeInTheDocument();
  });

  it('summarizes each file with cwd-relative paths and nonzero line counts', () => {
    const diff = [
      'diff --git a/packages/app/src/file one.ts b/packages/app/src/file one.ts',
      'index 1111111..2222222 100644',
      '--- a/packages/app/src/file one.ts\t',
      '+++ b/packages/app/src/file one.ts\t',
      '@@ -1,2 +1,3 @@',
      '-old',
      '+new',
      '+added',
      ' context',
      'diff --git a/README.md b/README.md',
      'index 3333333..4444444 100644',
      '--- a/README.md',
      '+++ b/README.md',
      '@@ -1,2 +0,0 @@',
      '-one',
      '-two',
    ].join('\n');

    render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/workspace/repo/packages/app',
          diff,
          exit_code: 0,
          git_root: '/workspace/repo',
          has_diff: true,
        }}
        loading={false}
        onRefresh={vi.fn()}
        open
      />
    );

    const firstFileToggle = screen.getByRole('button', { name: 'Expand src/file one.ts' });
    expect(firstFileToggle).toHaveAttribute('aria-expanded', 'false');
    const statsId = firstFileToggle.getAttribute('aria-describedby');
    expect(statsId).not.toBeNull();
    expect(document.getElementById(statsId || '')).toHaveTextContent('+2-1');
    expect(screen.getByRole('button', { name: 'Expand ../../README.md' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
    expect(screen.getByText('+2')).toBeInTheDocument();
    expect(screen.getByText('-1')).toBeInTheDocument();
    expect(screen.getByText('-2')).toBeInTheDocument();
    expect(screen.queryByText('+0')).not.toBeInTheDocument();
    expect(screen.queryByText('-0')).not.toBeInTheDocument();
    expect(screen.queryByText('old')).not.toBeInTheDocument();
    expect(screen.queryByText('one')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Expand src/file one.ts' }));

    expect(screen.getByText('new')).toBeInTheDocument();
    expect(screen.getByText('added')).toBeInTheDocument();
    expect(screen.queryByText('one')).not.toBeInTheDocument();
  });

  it('expands and collapses all file diffs from the toolbar toggle', () => {
    const diff = [
      'diff --git a/one.txt b/one.txt',
      '--- a/one.txt',
      '+++ b/one.txt',
      '@@ -1 +1 @@',
      '-old one',
      '+new one',
      'diff --git a/two.txt b/two.txt',
      '--- a/two.txt',
      '+++ b/two.txt',
      '@@ -1 +1 @@',
      '-old two',
      '+new two',
    ].join('\n');

    render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/workspace/repo',
          diff,
          exit_code: 0,
          git_root: '/workspace/repo',
          has_diff: true,
        }}
        loading={false}
        onRefresh={vi.fn()}
        open
      />
    );

    const expandAll = screen.getByRole('button', { name: 'Expand all file diffs' });
    expect(expandAll).toHaveAttribute('aria-pressed', 'false');
    expect(screen.queryByText('new one')).not.toBeInTheDocument();
    expect(screen.queryByText('new two')).not.toBeInTheDocument();

    fireEvent.click(expandAll);

    const collapseAll = screen.getByRole('button', { name: 'Collapse all file diffs' });
    expect(collapseAll).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('new one')).toBeInTheDocument();
    expect(screen.getByText('new two')).toBeInTheDocument();

    fireEvent.click(collapseAll);

    expect(screen.getByRole('button', { name: 'Expand all file diffs' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
    expect(screen.queryByText('new one')).not.toBeInTheDocument();
    expect(screen.queryByText('new two')).not.toBeInTheDocument();
  });

  it('keeps nested submodule diffs together and includes combined conflict diffs', () => {
    const diff = [
      'Submodule nested contains modified content',
      'Submodule child 1234567..abcdef0:',
      'diff --git a/nested/child/file.txt b/nested/child/file.txt',
      'index 1111111..2222222 100644',
      '--- a/nested/child/file.txt',
      '+++ b/nested/child/file.txt',
      '@@ -1 +1 @@',
      '-nested-old',
      '+nested-new',
      'diff --cc conflict.txt',
      'index 3333333,4444444..0000000',
      '--- a/conflict.txt',
      '+++ b/conflict.txt',
      '@@@ -1,1 -1,1 +1,5 @@@',
      '++<<<<<<< HEAD',
      ' +master',
      '++=======',
      '+ other',
      '++>>>>>>> other',
    ].join('\n');

    const { container } = render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/workspace/repo',
          diff,
          exit_code: 0,
          git_root: '/workspace/repo',
          has_diff: true,
        }}
        loading={false}
        onRefresh={vi.fn()}
        open
      />
    );

    expect(container.querySelectorAll('.workspace-diff-file')).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Expand nested' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Expand child' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /nested\/child\/file\.txt/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Expand conflict.txt' })).toBeInTheDocument();
    expect(screen.getByText('+4')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Expand nested' }));
    expect(screen.getByText('nested-old')).toBeInTheDocument();
    expect(screen.getByText('nested-new')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Expand conflict.txt' }));
    expect(screen.getByText('+<<<<<<< HEAD')).toBeInTheDocument();
    expect(screen.getByText('+>>>>>>> other')).toBeInTheDocument();
  });

  it('handles ambiguous mode-only paths and root-relative cwd paths', () => {
    const diff = [
      'diff --git a/workspace/repo/foo b/bar b/workspace/repo/foo b/bar',
      'old mode 100644',
      'new mode 100755',
      'diff --git a/workspace/repo/trailing  b/workspace/repo/trailing ',
      'index 1111111..2222222 100644',
      '--- a/workspace/repo/trailing \t',
      '+++ b/workspace/repo/trailing \t',
      '@@ -1 +1 @@',
      '-old',
      '+new',
      'diff --git "a/workspace/repo/sub\\\\dir/file.txt" "b/workspace/repo/sub\\\\dir/file.txt"',
      'index 3333333..4444444 100644',
      '--- "a/workspace/repo/sub\\\\dir/file.txt"',
      '+++ "b/workspace/repo/sub\\\\dir/file.txt"',
      '@@ -1 +1 @@',
      '-before',
      '+after',
      'diff --git a/workspace/repo/empty.txt b/workspace/repo/empty.txt',
      'deleted file mode 100644',
      'index e69de29..0000000',
    ].join('\n');

    const { container } = render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/workspace/repo',
          diff,
          exit_code: 0,
          git_root: '/',
          has_diff: true,
        }}
        loading={false}
        onRefresh={vi.fn()}
        open
      />
    );

    expect(screen.getByRole('button', { name: 'Expand foo b/bar' })).toBeInTheDocument();
    expect(screen.getByText('Mode changed')).toBeInTheDocument();
    const trailingPath = container.querySelector('[title="trailing "]');
    expect(trailingPath).not.toBeNull();
    expect(trailingPath?.textContent).toBe('trailing\\x20');
    expect(screen.getByRole('button', { name: 'Expand trailing\\x20' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Expand sub\\\\dir/file.txt' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Expand empty.txt' })).toBeInTheDocument();
    expect(screen.getByText('Deleted')).toBeInTheDocument();
  });

  it('preserves POSIX backslashes when making paths relative to cwd', () => {
    const diff = [
      'diff --git "a/sub\\\\dir/file.txt" "b/sub\\\\dir/file.txt"',
      'index 1111111..2222222 100644',
      '--- "a/sub\\\\dir/file.txt"',
      '+++ "b/sub\\\\dir/file.txt"',
      '@@ -1 +1 @@',
      '-old',
      '+new',
    ].join('\n');

    render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/workspace/repo/sub\\dir',
          diff,
          exit_code: 0,
          git_root: '/workspace/repo',
          has_diff: true,
        }}
        loading={false}
        onRefresh={vi.fn()}
        open
      />
    );

    expect(screen.getByRole('button', { name: 'Expand file.txt' })).toBeInTheDocument();
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
    expect(screen.getByRole('button', { name: 'Expand all file diffs' })).toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: 'Expand one.txt' }));
    fireEvent.click(screen.getByRole('button', { name: 'Expand two.txt' }));

    const addedContents = Array.from(container.querySelectorAll('.diff-line-added .diff-content'))
      .map((element) => element.textContent);
    const removedContents = Array.from(container.querySelectorAll('.diff-line-removed .diff-content'))
      .map((element) => element.textContent);

    expect(addedContents).toEqual(['new one', 'new two']);
    expect(removedContents).toEqual(['old one', 'old two']);
  });

  it('copies the full diff and refreshes from the toolbar actions', () => {
    const diff = 'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new';
    const onRefresh = vi.fn();

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
        onRefresh={onRefresh}
        open
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy diff' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh diff' }));

    expect(copyToClipboardMock).toHaveBeenCalledWith(diff);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('keeps refresh available when there is no diff', () => {
    const onRefresh = vi.fn();

    render(
      <GitDiffModal
        error={null}
        gitDiff={{
          cwd: '/tmp/project',
          diff: '',
          exit_code: 0,
          git_root: '/tmp/project',
          has_diff: false,
        }}
        loading={false}
        onRefresh={onRefresh}
        open
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Refresh diff' }));

    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: 'Copy diff' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Expand all file diffs' })).not.toBeInTheDocument();
  });
});
