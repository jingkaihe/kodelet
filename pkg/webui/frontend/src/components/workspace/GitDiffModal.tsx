import React from 'react';
import { copyToClipboard, truncateMiddle } from '../../utils';
import { parseUnifiedDiff, ReferenceDiffBlock } from '../tool-renderers/reference';
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
  const diffLines = diffText ? parseUnifiedDiff(diffText) : [];

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
            <h2 className="workspace-modal-title">Git diff</h2>
            <p className="workspace-modal-copy" title={cwdLabel}>
              {truncateMiddle(cwdLabel, 92)}
            </p>
          </div>

          <div className="workspace-modal-actions">
            {gitDiff?.has_diff && !loading ? (
              <button className="composer-capsule" onClick={() => void copyToClipboard(diffText)} type="button">
                Copy diff
              </button>
            ) : null}
            <button className="composer-capsule" onClick={onRefresh} type="button">
              Refresh
            </button>
            <button className="composer-capsule" onClick={onClose} type="button">
              Close
            </button>
          </div>
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
            <div className="workspace-modal-scroll-region" data-testid="git-diff-content">
              <ReferenceDiffBlock lines={diffLines} />
            </div>
          ) : (
            <div className="workspace-modal-placeholder">No working tree changes in this repository.</div>
          )}
        </div>
      </div>
    </div>
  );
};

export default GitDiffModal;
