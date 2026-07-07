import React from 'react';
import { ApplyPatchChange, ApplyPatchMetadata, ToolResult } from '../../types';
import {
  parseUnifiedDiff,
  ReferenceDiffBlock,
  ReferenceDiffLine,
} from './reference';

interface ApplyPatchRendererProps {
  toolResult: ToolResult;
}

const buildDiffLines = (change: ApplyPatchChange): ReferenceDiffLine[] => {
  return change.unifiedDiff ? parseUnifiedDiff(change.unifiedDiff) : [];
};

const normalizeOperation = (operation?: string): string => (operation || 'update').toLowerCase();

const getOperationLabel = (operation: string): string => {
  switch (operation) {
    case 'add':
    case 'write':
      return 'Write';
    case 'delete':
      return 'Delete';
    case 'move':
      return 'Move';
    case 'update':
      return 'Edit';
    default:
      return operation.charAt(0).toUpperCase() + operation.slice(1);
  }
};

const lineCounts = (lines: ReferenceDiffLine[]): { added: number; removed: number } => ({
  added: lines.filter((line) => line.kind === 'added').length,
  removed: lines.filter((line) => line.kind === 'removed').length,
});

const ApplyPatchRenderer: React.FC<ApplyPatchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ApplyPatchMetadata;
  if (!meta) return null;

  const changes = meta.changes || [];

  return (
    <div className={`apply-patch-result${!toolResult.success ? ' apply-patch-result-failed' : ''}`}>
      {!toolResult.success && toolResult.error ? <div className="apply-patch-error">{toolResult.error}</div> : null}
      {changes.length > 0 ? (
        changes.map((change, index) => {
          const diffLines = buildDiffLines(change);
          const displayPath = change.movePath ? `${change.path} → ${change.movePath}` : change.path;
          const operation = change.movePath ? 'move' : normalizeOperation(change.operation);
          const counts = lineCounts(diffLines);

          return (
            <div key={`${change.path}-${change.operation}-${index}`} className="apply-patch-change">
              <div className="apply-patch-change-line">
                <span className={`apply-patch-operation apply-patch-operation-${operation}`}>
                  {getOperationLabel(operation)}
                </span>
                <span className="apply-patch-path">{displayPath}</span>
                <span className="apply-patch-counts">
                  <span className="apply-patch-count-added">+{counts.added}</span>
                  <span className="apply-patch-count-removed">-{counts.removed}</span>
                </span>
              </div>
              {diffLines.length > 0 ? <ReferenceDiffBlock lines={diffLines} /> : null}
            </div>
          );
        })
      ) : (
        <div className="apply-patch-empty">
          {toolResult.success ? 'No files were modified.' : 'No file diffs were available.'}
        </div>
      )}
    </div>
  );
};

export default ApplyPatchRenderer;
