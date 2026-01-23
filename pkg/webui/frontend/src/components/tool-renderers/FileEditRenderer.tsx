import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, MetadataRow, Collapsible } from './shared';

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

  const renderEdits = (edits: FileEdit[]) => {
    return edits.map((edit: FileEdit, index: number) => {
      const oldContent = edit.oldContent || '';
      const newContent = edit.newContent || '';
      const filePath = meta.filePath || '';

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
            let foundMatch = false;

            for (let i = newIndex + 1; i < Math.min(newIndex + 3, newLines.length); i++) {
              if (oldLines[oldIndex] === newLines[i]) {
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
              for (let i = oldIndex + 1; i < Math.min(oldIndex + 3, oldLines.length); i++) {
                if (newLines[newIndex] === oldLines[i]) {
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
          <h5 className="text-sm font-heading font-medium mb-2 text-kodelet-dark">
            Edit {index + 1}: Lines {edit.startLine}-{edit.endLine}
          </h5>
          <div className="bg-kodelet-light border border-kodelet-light-gray rounded-lg overflow-hidden font-mono text-sm">
            <div className="bg-kodelet-light-gray/50 px-4 py-2 text-kodelet-dark/70 border-b border-kodelet-light-gray">
              <div>--- a/{filePath.split('/').pop()}</div>
              <div>+++ b/{filePath.split('/').pop()}</div>
            </div>

            <div className="bg-kodelet-blue/10 px-4 py-1 text-kodelet-blue border-b border-kodelet-light-gray">
              @@ -{edit.startLine},{oldLineCount} +{edit.startLine},{newLineCount} @@
            </div>

            <div>
              {diffLines.map((line, i) => {
                const bgColor = line.type === 'removed' ? 'bg-kodelet-orange/10' :
                  line.type === 'added' ? 'bg-kodelet-green/10' : 'bg-white';
                const textColor = line.type === 'removed' ? 'text-kodelet-orange' :
                  line.type === 'added' ? 'text-kodelet-green' : 'text-kodelet-dark';
                const prefix = line.type === 'removed' ? '-' :
                  line.type === 'added' ? '+' : ' ';
                const prefixColor = line.type === 'removed' ? 'text-kodelet-orange' :
                  line.type === 'added' ? 'text-kodelet-green' : 'text-kodelet-mid-gray';

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

  const getTitle = () => {
    if (replaceAll && replacedCount > 1) {
      return "File Edit (Replace All)";
    }
    return "File Edit";
  };

  const getBadge = () => {
    const text = replaceAll 
      ? `${replacedCount} replacement${replacedCount !== 1 ? 's' : ''}`
      : `${edits.length} edit${edits.length !== 1 ? 's' : ''}`;
    return {
      text,
      className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20'
    };
  };

  const getCollapsibleTitle = () => {
    return replaceAll && edits.length > 1 ? "All Changes" : "Changes";
  };

  const getCollapsibleBadge = () => {
    const text = replaceAll && replacedCount > 0 
      ? `${edits.length} locations`
      : `${edits.length} changes`;
    return {
      text,
      className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-dark/10 text-kodelet-dark border border-kodelet-dark/20'
    };
  };

  return (
    <ToolCard
      title={getTitle()}
      badge={getBadge()}
    >
      <div className="text-xs text-kodelet-dark/60 mb-3 font-mono">
        <MetadataRow label="Path" value={meta.filePath} monospace />
        {replaceAll && (
          <MetadataRow label="Mode" value="Replace All" />
        )}
      </div>

      {edits.length > 0 && (
        <Collapsible
          title={getCollapsibleTitle()}
          collapsed={false}
          badge={getCollapsibleBadge()}
        >
          <div>
            {replaceAll && edits.length > 3 && (
              <div className="text-xs text-kodelet-dark/60 mb-2 font-body">
                Showing all {edits.length} replacement locations:
              </div>
            )}
            {renderEdits(edits)}
          </div>
        </Collapsible>
      )}
    </ToolCard>
  );
};

export default FileEditRenderer;
