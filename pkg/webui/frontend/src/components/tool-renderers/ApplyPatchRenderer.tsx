import React, { useState } from 'react';
import { ApplyPatchChange, ApplyPatchMetadata, ToolResult } from '../../types';
import { StatusBadge } from './shared';

interface ApplyPatchRendererProps {
  toolResult: ToolResult;
}

type DiffLineType = 'context' | 'added' | 'removed' | 'header' | 'meta';

interface DiffLine {
  type: DiffLineType;
  content: string;
}

const stripTrailingEmptyLine = (text: string): string[] => {
  const lines = text.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    return lines.slice(0, -1);
  }
  return lines;
};

const buildFallbackDiff = (change: ApplyPatchChange): DiffLine[] => {
  if (change.operation === 'add') {
    return (change.newContent ? stripTrailingEmptyLine(change.newContent) : []).map((line) => ({
      type: 'added',
      content: `+${line}`,
    }));
  }

  if (change.operation === 'delete') {
    return (change.oldContent ? stripTrailingEmptyLine(change.oldContent) : []).map((line) => ({
      type: 'removed',
      content: `-${line}`,
    }));
  }

  const removed = (change.oldContent ? stripTrailingEmptyLine(change.oldContent) : []).map((line) => ({
    type: 'removed' as const,
    content: `-${line}`,
  }));
  const added = (change.newContent ? stripTrailingEmptyLine(change.newContent) : []).map((line) => ({
    type: 'added' as const,
    content: `+${line}`,
  }));

  return [...removed, ...added];
};

const buildDiffLines = (change: ApplyPatchChange): DiffLine[] => {
  if (!change.unifiedDiff) {
    return buildFallbackDiff(change);
  }

  return change.unifiedDiff.split('\n').map((line) => {
    if (line.startsWith('@@')) {
      return { type: 'header', content: line };
    }
    if (line.startsWith('---') || line.startsWith('+++')) {
      return { type: 'meta', content: line };
    }
    if (line.startsWith('+')) {
      return { type: 'added', content: line };
    }
    if (line.startsWith('-')) {
      return { type: 'removed', content: line };
    }
    return { type: 'context', content: line };
  });
};

const changeBadgeVariant = (operation: string): 'success' | 'error' | 'info' => {
  if (operation === 'add') return 'success';
  if (operation === 'delete') return 'error';
  return 'info';
};

const changeLabel = (operation: string): string => {
  if (operation === 'add') return 'Added';
  if (operation === 'delete') return 'Deleted';
  if (operation === 'update') return 'Updated';
  return operation;
};

const lineClassName = (type: DiffLineType): string => {
  if (type === 'added') return 'bg-green-50 text-green-700';
  if (type === 'removed') return 'bg-red-50 text-red-700';
  if (type === 'header') return 'bg-kodelet-light-gray/50 text-kodelet-dark/70';
  if (type === 'meta') return 'text-kodelet-mid-gray';
  return '';
};

const ApplyPatchRenderer: React.FC<ApplyPatchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ApplyPatchMetadata;
  const [showDiffs, setShowDiffs] = useState(false);
  if (!meta) return null;

  const added = meta.added || [];
  const modified = meta.modified || [];
  const deleted = meta.deleted || [];
  const changes = meta.changes || [];

  const fileCountFromSummary = added.length + modified.length + deleted.length;
  const fileCount = fileCountFromSummary > 0 ? fileCountFromSummary : changes.length;

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
        <StatusBadge
          text={`${fileCount} file${fileCount === 1 ? '' : 's'}`}
          variant="success"
        />
        {added.length > 0 && <StatusBadge text={`A ${added.length}`} variant="success" />}
        {modified.length > 0 && <StatusBadge text={`M ${modified.length}`} variant="info" />}
        {deleted.length > 0 && <StatusBadge text={`D ${deleted.length}`} variant="error" />}
      </div>

      {(added.length > 0 || modified.length > 0 || deleted.length > 0) && (
        <div className="space-y-1 text-xs font-mono text-kodelet-dark/80">
          {added.map((path) => (
            <div key={`add-${path}`} className="flex gap-2">
              <span className="text-green-700">A</span>
              <span className="break-all">{path}</span>
            </div>
          ))}
          {modified.map((path) => (
            <div key={`mod-${path}`} className="flex gap-2">
              <span className="text-kodelet-blue">M</span>
              <span className="break-all">{path}</span>
            </div>
          ))}
          {deleted.map((path) => (
            <div key={`del-${path}`} className="flex gap-2">
              <span className="text-red-700">D</span>
              <span className="break-all">{path}</span>
            </div>
          ))}
        </div>
      )}

      {changes.length > 0 && (
        <>
          {!showDiffs ? (
            <button
              onClick={() => setShowDiffs(true)}
              className="text-xs text-kodelet-blue hover:underline"
            >
              Show diffs ({changes.length})
            </button>
          ) : (
            <div className="space-y-2" style={{ maxHeight: '420px', overflowY: 'auto' }}>
              {changes.map((change, index) => {
                const diffLines = buildDiffLines(change);
                const displayPath = change.movePath
                  ? `${change.path} -> ${change.movePath}`
                  : change.path;

                return (
                  <div
                    key={`${change.path}-${change.operation}-${index}`}
                    className="text-xs font-mono border border-kodelet-light-gray rounded overflow-hidden"
                  >
                    <div className="bg-kodelet-light-gray/50 px-2 py-1 text-kodelet-dark/70 flex items-center justify-between gap-2">
                      <span className="break-all">{displayPath}</span>
                      <StatusBadge text={changeLabel(change.operation)} variant={changeBadgeVariant(change.operation)} />
                    </div>
                    <div>
                      {diffLines.length > 0 ? (
                        diffLines.map((line, lineIndex) => (
                          <div
                            key={`${change.path}-${index}-${lineIndex}`}
                            className={`px-2 py-0.5 flex ${lineClassName(line.type)}`}
                          >
                            <span className="whitespace-pre-wrap break-words">{line.content || '\u00A0'}</span>
                          </div>
                        ))
                      ) : (
                        <div className="px-2 py-1 text-kodelet-mid-gray">No diff content</div>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default ApplyPatchRenderer;
