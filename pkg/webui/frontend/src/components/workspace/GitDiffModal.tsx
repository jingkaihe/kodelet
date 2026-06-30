import React from 'react';
import { Copy, RefreshCw } from 'lucide-react';
import { copyToClipboard } from '../../utils';
import { parseUnifiedDiff, ReferenceDiffBlock } from '../tool-renderers/reference';
import type { GitDiffResponse } from '../../types';

interface GitDiffModalProps {
  cwdLabel?: string;
  error: string | null;
  gitDiff: GitDiffResponse | null;
  loading: boolean;
  onClose?: () => void;
  open: boolean;
  onRefresh: () => void;
}

const GitDiffModal: React.FC<GitDiffModalProps> = ({
  error,
  gitDiff,
  loading,
  open,
  onRefresh,
}) => {
  if (!open) {
    return null;
  }

  const diffText = gitDiff?.diff || '';
  const diffLines = diffText ? parseUnifiedDiff(diffText) : [];

  return (
    <section
      aria-label="Changes"
      className="workspace-side-panel workspace-diff-panel surface-panel"
      data-testid="git-diff-panel"
      role="complementary"
    >
      {error ? (
        <div className="surface-panel rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark" role="alert">
          {error}
        </div>
      ) : null}

      <div className="workspace-modal-body workspace-side-panel-body">
        {loading ? (
          <div className="workspace-modal-placeholder">Loading diff…</div>
        ) : gitDiff?.has_diff ? (
          <div className="workspace-modal-scroll-region" data-testid="git-diff-content">
            <div className="workspace-diff-floating-actions" aria-label="Diff actions">
              <button
                aria-label="Copy diff"
                className="workspace-diff-icon-button"
                onClick={() => void copyToClipboard(diffText)}
                title="Copy diff"
                type="button"
              >
                <Copy aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
              </button>
              <button
                aria-label="Refresh diff"
                className="workspace-diff-icon-button"
                onClick={onRefresh}
                title="Refresh diff"
                type="button"
              >
                <RefreshCw aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
              </button>
            </div>
            <ReferenceDiffBlock lines={diffLines} />
          </div>
        ) : (
          <div className="workspace-modal-placeholder">No working tree changes in this repository.</div>
        )}
      </div>
    </section>
  );
};

export default GitDiffModal;
