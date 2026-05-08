import React from 'react';
import { ApplyPatchChange, ApplyPatchMetadata, ToolResult } from '../../types';
import {
  ReferenceDiffBlock,
  ReferenceDiffLine,
} from './reference';

interface ApplyPatchRendererProps {
  toolResult: ToolResult;
}

const stripTrailingEmptyLine = (text: string): string[] => {
  const lines = text.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    return lines.slice(0, -1);
  }
  return lines;
};

const buildFallbackDiff = (change: ApplyPatchChange): ReferenceDiffLine[] => {
  if (change.operation === 'add') {
    return (change.newContent ? stripTrailingEmptyLine(change.newContent) : []).map((line) => ({
      kind: 'added',
      content: line,
    }));
  }

  if (change.operation === 'delete') {
    return (change.oldContent ? stripTrailingEmptyLine(change.oldContent) : []).map((line) => ({
      kind: 'removed',
      content: line,
    }));
  }

  const removed = (change.oldContent ? stripTrailingEmptyLine(change.oldContent) : []).map((line) => ({
    kind: 'removed' as const,
    content: line,
  }));
  const added = (change.newContent ? stripTrailingEmptyLine(change.newContent) : []).map((line) => ({
    kind: 'added' as const,
    content: line,
  }));

  return [...removed, ...added];
};

const buildDiffLines = (change: ApplyPatchChange): ReferenceDiffLine[] => {
  if (!change.unifiedDiff) {
    return buildFallbackDiff(change);
  }

  let seenHunk = false;

  return change.unifiedDiff
    .split('\n')
    .map((line): ReferenceDiffLine | null => {
      if (!seenHunk && (line.startsWith('--- ') || line.startsWith('+++ '))) {
        return null;
      }
      if (line.startsWith('@@')) {
        seenHunk = true;
        return { kind: 'header', content: line };
      }
      if (line.startsWith('+')) {
        return { kind: 'added', content: line.slice(1) };
      }
      if (line.startsWith('-')) {
        return { kind: 'removed', content: line.slice(1) };
      }
      if (line.startsWith(' ')) {
        return { kind: 'context', content: line.slice(1) };
      }
      return { kind: 'context', content: line };
    })
    .filter((line): line is ReferenceDiffLine => line !== null);
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
      return 'Update';
    default:
      return operation.charAt(0).toUpperCase() + operation.slice(1);
  }
};

const buildListOnlyChanges = (
  added: string[],
  modified: string[],
  deleted: string[]
): ApplyPatchChange[] => [
  ...added.map((path) => ({ path, operation: 'add' })),
  ...modified.map((path) => ({ path, operation: 'update' })),
  ...deleted.map((path) => ({ path, operation: 'delete' })),
];

const ApplyPatchRenderer: React.FC<ApplyPatchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ApplyPatchMetadata;
  if (!meta) return null;

  const added = meta.added || [];
  const modified = meta.modified || [];
  const deleted = meta.deleted || [];
  const changes = meta.changes || [];
  const displayChanges = changes.length > 0
    ? changes
    : buildListOnlyChanges(added, modified, deleted);

  return (
    <div className="apply-patch-result">
      {displayChanges.length > 0 ? (
        displayChanges.map((change, index) => {
          const displayPath = change.movePath ? `${change.path} -> ${change.movePath}` : change.path;
          const operation = normalizeOperation(change.operation);
          const diffLines = buildDiffLines(change);

          return (
            <div key={`${change.path}-${change.operation}-${index}`} className="apply-patch-change">
              <div className="apply-patch-change-line">
                <span className={`apply-patch-operation apply-patch-operation-${operation}`}>
                  {getOperationLabel(operation)}
                </span>
                <span className="apply-patch-path">{displayPath}</span>
              </div>
              {diffLines.length > 0 ? <ReferenceDiffBlock lines={diffLines} /> : null}
            </div>
          );
        })
      ) : (
        <div className="apply-patch-empty">patch applied</div>
      )}
    </div>
  );
};

export default ApplyPatchRenderer;
