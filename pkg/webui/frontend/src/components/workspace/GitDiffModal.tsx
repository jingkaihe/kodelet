import React from 'react';
import { copyToClipboard, truncateMiddle } from '../../utils';
import { compactDiffLines, parseUnifiedDiff, ReferenceDiffBlock } from '../tool-renderers/reference';
import type { GitDiffResponse } from '../../types';

interface GitDiffModalProps {
  cwdLabel: string;
  error: string | null;
  gitDiff: GitDiffResponse | null;
  loading: boolean;
  onClose: () => void;
  open: boolean;
  onRefresh: () => void;
}

const GitDiffModal: React.FC<GitDiffModalProps> = ({
  cwdLabel,
  error,
  gitDiff,
  loading,
  onClose,
  open,
  onRefresh,
}) => {
  if (!open) {
    return null;
  }

  const diffText = gitDiff?.diff || '';
  const diffLines = diffText ? compactDiffLines(parseUnifiedDiff(diffText), 2, 2, 220) : [];

  return (
    <div className="workspace-modal-backdrop" data-testid="git-diff-modal-backdrop">
      <div
        aria-label="Git diff"
        className="workspace-modal workspace-modal-wide surface-panel"
        data-testid="git-diff-modal"
        role="dialog"
      >
        <div className="workspace-modal-header">
          <div className="workspace-modal-heading-group">
            <p className="eyebrow-label text-kodelet-mid-gray">Workspace</p>
            <h2 className="workspace-modal-title">Git diff</h2>
            <p className="workspace-modal-copy" title={cwdLabel}>
              {truncateMiddle(cwdLabel, 92)}
            </p>
          </div>

          <div className="workspace-modal-actions">
            <button className="composer-capsule" onClick={onRefresh} type="button">
              Refresh
            </button>
            <button className="composer-capsule" onClick={onClose} type="button">
              Close
            </button>
          </div>
        </div>

        <div className="workspace-modal-meta">
          <span className="workspace-modal-meta-pill">{gitDiff?.has_diff ? 'Uncommitted changes' : 'Working tree clean'}</span>
          {gitDiff?.git_root ? (
            <span className="workspace-modal-meta-text" title={gitDiff.git_root}>
              Repo {truncateMiddle(gitDiff.git_root, 84)}
            </span>
          ) : null}
          {gitDiff?.command ? <span className="workspace-modal-meta-text mono">{gitDiff.command}</span> : null}
        </div>

        {error ? (
          <div className="surface-panel rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark" role="alert">
            {error}
          </div>
        ) : null}

        <div className="workspace-modal-body">
          {loading ? (
            <div className="workspace-modal-placeholder">Loading diff…</div>
          ) : gitDiff?.has_diff ? (
            <>
              <div className="workspace-modal-toolbar">
                <button className="tool-action-link" onClick={() => void copyToClipboard(diffText)} type="button">
                  Copy diff
                </button>
              </div>
              <div className="workspace-modal-scroll-region" data-testid="git-diff-content">
                <ReferenceDiffBlock lines={diffLines} />
              </div>
            </>
          ) : (
            <div className="workspace-modal-placeholder">No working tree changes in this repository.</div>
          )}
        </div>
      </div>
    </div>
  );
};

export default GitDiffModal;
