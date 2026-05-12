import React from 'react';
import { ToolResult } from '../../types';
import {
  compactDiffLines,
  ReferenceDiffBlock,
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
  const replacedCount = meta.replacedCount || meta.actualReplaced || edits.length;

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

  const replacementText = `${replacedCount} replacement${replacedCount !== 1 ? 's' : ''}`;

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{replacementText}</span>
        <span className="quiet-tool-muted">{replaceAll ? 'replace all' : 'targeted edit'}</span>
      </div>
      <div className="quiet-tool-path">{meta.filePath}</div>

      <div className="space-y-3">
        {edits.map((edit, index) => {
          const diffLines =
            edit.oldContent || edit.newContent
              ? compactDiffLines(createUnifiedDiff(edit.oldContent || '', edit.newContent || ''))
              : [];

          return (
            <div key={index} className="space-y-2">
              <div className="quiet-tool-section-title">
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
