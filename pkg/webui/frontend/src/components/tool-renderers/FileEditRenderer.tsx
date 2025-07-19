import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, MetadataRow, Collapsible } from './shared';

interface FileEditMetadata {
  filePath: string;
  edits: FileEdit[];
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


  const renderEdits = (edits: FileEdit[]) => {
    return edits.map((edit: FileEdit, index: number) => {
      const oldContent = edit.oldContent || '';
      const newContent = edit.newContent || '';
      const filePath = meta.filePath || '';

      // Create unified diff with improved algorithm
      const createUnifiedDiff = (oldText: string, newText: string) => {
        const oldLines = oldText ? oldText.split('\n') : [];
        const newLines = newText ? newText.split('\n') : [];
        const diffLines: Array<{ type: 'context' | 'removed' | 'added', content: string }> = [];

        // Simple diff algorithm that can handle line insertions/deletions
        let oldIndex = 0;
        let newIndex = 0;

        while (oldIndex < oldLines.length || newIndex < newLines.length) {
          if (oldIndex >= oldLines.length) {
            // Only new lines remaining
            diffLines.push({ type: 'added', content: newLines[newIndex] });
            newIndex++;
          } else if (newIndex >= newLines.length) {
            // Only old lines remaining
            diffLines.push({ type: 'removed', content: oldLines[oldIndex] });
            oldIndex++;
          } else if (oldLines[oldIndex] === newLines[newIndex]) {
            // Lines match - context
            diffLines.push({ type: 'context', content: oldLines[oldIndex] });
            oldIndex++;
            newIndex++;
          } else {
            // Lines differ - look ahead to see if we can find matches
            let foundMatch = false;

            // Look for the current old line in upcoming new lines (deletion)
            for (let i = newIndex + 1; i < Math.min(newIndex + 3, newLines.length); i++) {
              if (oldLines[oldIndex] === newLines[i]) {
                // Found the old line later in new lines, so current new lines are insertions
                for (let j = newIndex; j < i; j++) {
                  diffLines.push({ type: 'added', content: newLines[j] });
                }
                diffLines.push({ type: 'context', content: oldLines[oldIndex] });
                newIndex = i + 1;
                oldIndex++;
                foundMatch = true;
                break;
              }
            }

            if (!foundMatch) {
              // Look for the current new line in upcoming old lines (insertion)
              for (let i = oldIndex + 1; i < Math.min(oldIndex + 3, oldLines.length); i++) {
                if (newLines[newIndex] === oldLines[i]) {
                  // Found the new line later in old lines, so current old lines are deletions
                  for (let j = oldIndex; j < i; j++) {
                    diffLines.push({ type: 'removed', content: oldLines[j] });
                  }
                  diffLines.push({ type: 'context', content: newLines[newIndex] });
                  oldIndex = i + 1;
                  newIndex++;
                  foundMatch = true;
                  break;
                }
              }
            }

            if (!foundMatch) {
              // No match found, treat as substitution
              diffLines.push({ type: 'removed', content: oldLines[oldIndex] });
              diffLines.push({ type: 'added', content: newLines[newIndex] });
              oldIndex++;
              newIndex++;
            }
          }
        }

        return diffLines;
      };

      const diffLines = createUnifiedDiff(oldContent, newContent);
      const oldLineCount = oldContent ? oldContent.split('\n').length : 0;
      const newLineCount = newContent ? newContent.split('\n').length : 0;

      return (
        <div key={index} className="mb-4">
          <h5 className="text-sm font-medium mb-2">Edit {index + 1}: Lines {edit.startLine}-{edit.endLine}</h5>
          <div className="bg-gray-50 border border-gray-200 rounded-lg overflow-hidden font-mono text-sm">
            {/* Git diff header */}
            <div className="bg-gray-100 px-4 py-2 text-gray-600 border-b border-gray-200">
              <div>--- a/{filePath.split('/').pop()}</div>
              <div>+++ b/{filePath.split('/').pop()}</div>
            </div>

            {/* Hunk header */}
            <div className="bg-cyan-50 px-4 py-1 text-cyan-700 border-b border-gray-200">
              @@ -{edit.startLine},{oldLineCount} +{edit.startLine},{newLineCount} @@
            </div>

            {/* Unified diff content */}
            <div>
              {diffLines.map((line, i) => {
                const bgColor = line.type === 'removed' ? 'bg-red-50' :
                  line.type === 'added' ? 'bg-green-50' : 'bg-white';
                const textColor = line.type === 'removed' ? 'text-red-800' :
                  line.type === 'added' ? 'text-green-800' : 'text-gray-800';
                const prefix = line.type === 'removed' ? '-' :
                  line.type === 'added' ? '+' : ' ';
                const prefixColor = line.type === 'removed' ? 'text-red-500' :
                  line.type === 'added' ? 'text-green-500' : 'text-gray-400';

                return (
                  <div key={i} className={`px-4 py-1 flex items-start ${bgColor}`}>
                    <span className={`mr-3 select-none ${prefixColor}`}>{prefix}</span>
                    <span className={`flex-1 whitespace-pre ${textColor}`}>{line.content}</span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      );
    });
  };



  return (
    <ToolCard
      title="✏️ File Edit"
      badge={{ text: `${edits.length} edit${edits.length !== 1 ? 's' : ''}`, className: 'badge-info' }}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <MetadataRow label="Path" value={meta.filePath} monospace />
      </div>

      {edits.length > 0 && (
        <Collapsible
          title="View Changes"
          collapsed={false}
          badge={{ text: `${edits.length} changes`, className: 'badge-info' }}
        >
          <div>{renderEdits(edits)}</div>
        </Collapsible>
      )}
    </ToolCard>
  );
};

export default FileEditRenderer;
