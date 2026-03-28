import React from 'react';
import { ApplyPatchChange, ApplyPatchMetadata, ToolResult } from '../../types';
import {
  compactDiffLines,
  parseUnifiedDiff,
  ReferenceCodeList,
  ReferenceDiffBlock,
  ReferenceDiffLine,
  ReferenceToolHeader,
  TOOL_ICONS,
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

  return change.unifiedDiff.split('\n').map((line) => {
    if (line.startsWith('@@')) {
      return { kind: 'header', content: line };
    }
    if (line.startsWith('---') || line.startsWith('+++')) {
      return { kind: 'meta', content: line };
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
  });
};

const buildFocusedDiffPreview = (lines: ReferenceDiffLine[]): ReferenceDiffLine[] => {
  const compacted = compactDiffLines(lines, 1, 1, Number.MAX_SAFE_INTEGER);
  const previewHead = 8;
  const previewTail = 6;

  if (compacted.length <= previewHead + previewTail + 1) {
    return compacted;
  }

  return [
    ...compacted.slice(0, previewHead),
    {
      kind: 'meta',
      content: `... ${compacted.length - previewHead - previewTail} more diff lines omitted ...`,
    },
    ...compacted.slice(-previewTail),
  ];
};

const titleCase = (value: string): string =>
  value ? `${value.charAt(0).toUpperCase()}${value.slice(1)}` : value;

const ApplyPatchRenderer: React.FC<ApplyPatchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ApplyPatchMetadata;
  if (!meta) return null;

  const added = meta.added || [];
  const modified = meta.modified || [];
  const deleted = meta.deleted || [];
  const changes = meta.changes || [];

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: `${changes.length} change${changes.length === 1 ? '' : 's'}`, variant: 'success' },
          { text: `A ${added.length}`, variant: 'success' },
          { text: `M ${modified.length}`, variant: 'info' },
          { text: `D ${deleted.length}`, variant: 'error' },
        ]}
        title={`${TOOL_ICONS.apply_patch} Apply Patch`}
      />

      {(added.length > 0 || modified.length > 0 || deleted.length > 0) ? (
        <ReferenceCodeList
          items={[
            ...added.map((path) => `A ${path}`),
            ...modified.map((path) => `M ${path}`),
            ...deleted.map((path) => `D ${path}`),
          ]}
        />
      ) : null}

      <div className="space-y-4">
        {changes.map((change, index) => {
          const displayPath = change.movePath ? `${change.path} -> ${change.movePath}` : change.path;
          const diffLines = buildFocusedDiffPreview(
            change.unifiedDiff ? parseUnifiedDiff(change.unifiedDiff) : buildDiffLines(change)
          );

          return (
            <div key={`${change.path}-${change.operation}-${index}`} className="space-y-2">
              <ReferenceToolHeader
                badges={[{ text: change.operation || 'update', variant: 'info' }]}
                subtitle={displayPath}
                title={`Change: ${titleCase(change.operation || 'update')}`}
              />
              <ReferenceDiffBlock lines={diffLines} />
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default ApplyPatchRenderer;
