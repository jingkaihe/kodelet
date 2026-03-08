import React from 'react';
import { ToolResult } from '../../types';
import {
  compactDiffLines,
  ReferenceDiffBlock,
  ReferenceToolHeader,
  TOOL_ICONS,
} from './reference';

interface FileEditMetadata {
  filePath: string;
  edits: FileEdit[];
  language?: string;
  replaceAll?: boolean;
  replacedCount?: number;
  actualReplaced?: number;
  occurrence?: number;
}

interface FileEdit {
  startLine: number;
  endLine: number;
  oldContent: string;
  newContent: string;
}

interface FileEditRendererProps {
  toolResult: ToolResult;
}

const FileEditRenderer: React.FC<FileEditRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileEditMetadata;
  if (!meta) return null;

  const edits = meta.edits || [];
  const replaceAll = meta.replaceAll || false;
  const replacedCount = meta.replacedCount || meta.actualReplaced || 0;

  const createUnifiedDiff = (oldText: string, newText: string) => {
    const oldLines = oldText ? oldText.split('\n') : [];
    const newLines = newText ? newText.split('\n') : [];
    const diffLines: Array<{ kind: 'context' | 'removed' | 'added'; content: string }> = [];

    let oldIndex = 0;
    let newIndex = 0;

    while (oldIndex < oldLines.length || newIndex < newLines.length) {
      if (oldIndex >= oldLines.length) {
        diffLines.push({ kind: 'added', content: newLines[newIndex] });
        newIndex++;
      } else if (newIndex >= newLines.length) {
        diffLines.push({ kind: 'removed', content: oldLines[oldIndex] });
        oldIndex++;
      } else if (oldLines[oldIndex] === newLines[newIndex]) {
        diffLines.push({ kind: 'context', content: oldLines[oldIndex] });
        oldIndex++;
        newIndex++;
      } else {
        diffLines.push({ kind: 'removed', content: oldLines[oldIndex] });
        diffLines.push({ kind: 'added', content: newLines[newIndex] });
        oldIndex++;
        newIndex++;
      }
    }
    return diffLines;
  };

  const badgeText = replaceAll
    ? `${replacedCount} replacement${replacedCount !== 1 ? 's' : ''}`
    : `${edits.length} edit${edits.length !== 1 ? 's' : ''}`;

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: badgeText, variant: 'info' },
          { text: replaceAll ? 'replace all' : 'targeted edit', variant: 'neutral' },
        ]}
        subtitle={meta.filePath}
        title={`${TOOL_ICONS.file_edit} File Edit`}
      />

      <div className="space-y-3">
        {edits.map((edit, index) => {
          const diffLines =
            edit.oldContent || edit.newContent
              ? compactDiffLines(createUnifiedDiff(edit.oldContent || '', edit.newContent || ''))
              : [];

          return (
            <div key={index} className="space-y-2">
              <div className="font-heading text-sm font-semibold text-kodelet-dark">
                Lines {edit.startLine}-{edit.endLine}
              </div>
              <ReferenceDiffBlock lines={diffLines} />
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default FileEditRenderer;
