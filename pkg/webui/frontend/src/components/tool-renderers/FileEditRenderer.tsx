import React, { useState } from 'react';
import { ToolResult } from '../../types';
import { StatusBadge } from './shared';

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
  const [showDiff, setShowDiff] = useState(false);
  if (!meta) return null;

  const edits = meta.edits || [];
  const replaceAll = meta.replaceAll || false;
  const replacedCount = meta.replacedCount || meta.actualReplaced || 0;

  const createUnifiedDiff = (oldText: string, newText: string) => {
    const oldLines = oldText ? oldText.split('\n') : [];
    const newLines = newText ? newText.split('\n') : [];
    const diffLines: Array<{ type: 'context' | 'removed' | 'added', content: string }> = [];

    let oldIndex = 0;
    let newIndex = 0;

    while (oldIndex < oldLines.length || newIndex < newLines.length) {
      if (oldIndex >= oldLines.length) {
        diffLines.push({ type: 'added', content: newLines[newIndex] });
        newIndex++;
      } else if (newIndex >= newLines.length) {
        diffLines.push({ type: 'removed', content: oldLines[oldIndex] });
        oldIndex++;
      } else if (oldLines[oldIndex] === newLines[newIndex]) {
        diffLines.push({ type: 'context', content: oldLines[oldIndex] });
        oldIndex++;
        newIndex++;
      } else {
        diffLines.push({ type: 'removed', content: oldLines[oldIndex] });
        diffLines.push({ type: 'added', content: newLines[newIndex] });
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
      <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
        <span className="font-medium">{meta.filePath}</span>
        <StatusBadge text={badgeText} variant="info" />
        {replaceAll && <span className="text-kodelet-mid-gray">(replace all)</span>}
      </div>

      {edits.length > 0 && (
        <>
          {!showDiff ? (
            <button 
              onClick={() => setShowDiff(true)}
              className="text-xs text-kodelet-blue hover:underline"
            >
              Show diff
            </button>
          ) : (
            <div className="space-y-2" style={{ maxHeight: '400px', overflowY: 'auto' }}>
              {edits.map((edit, index) => {
                const diffLines = createUnifiedDiff(edit.oldContent || '', edit.newContent || '');
                return (
                  <div key={index} className="text-xs font-mono border border-kodelet-light-gray rounded overflow-hidden">
                    <div className="bg-kodelet-light-gray/50 px-2 py-1 text-kodelet-dark/70">
                      Lines {edit.startLine}-{edit.endLine}
                    </div>
                    <div>
                      {diffLines.map((line, i) => (
                        <div 
                          key={i} 
                          className={`px-2 py-0.5 flex ${
                            line.type === 'removed' ? 'bg-red-50 text-red-700' :
                            line.type === 'added' ? 'bg-green-50 text-green-700' : ''
                          }`}
                        >
                          <span className="w-4 select-none">
                            {line.type === 'removed' ? '-' : line.type === 'added' ? '+' : ' '}
                          </span>
                          <span className="whitespace-pre">{line.content}</span>
                        </div>
                      ))}
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

export default FileEditRenderer;
