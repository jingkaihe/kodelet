import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, MetadataRow, Collapsible } from './shared';

interface FileEditRendererProps {
  toolResult: ToolResult;
}

const FileEditRenderer: React.FC<FileEditRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata;
  if (!meta) return null;

  const edits = meta.edits || [];
  const isMultiEdit = toolResult.toolName === 'file_multi_edit';
  const replacements = meta.actualReplaced || 0;

  const renderEdits = (edits: any[]) => {
    return edits.map((edit: any, index: number) => {
      const oldContent = edit.oldContent || '';
      const newContent = edit.newContent || '';

      return (
        <div key={index} className="mb-4">
          <h5 className="text-sm font-medium mb-2">Edit {index + 1}: Lines {edit.startLine}-{edit.endLine}</h5>
          <div className="mockup-code bg-gray-50 border border-gray-200 rounded-lg overflow-hidden">
            <div className="divide-y divide-gray-200">
              {/* Git diff style unified view */}
              <div className="p-4 bg-gray-100 text-xs text-gray-600 font-mono">
                @@ -{edit.startLine},{edit.endLine - edit.startLine + 1} +{edit.startLine},{edit.endLine - edit.startLine + 1} @@
              </div>
              {oldContent && (
                <div className="bg-red-50 border-l-4 border-red-400 p-3">
                  <div className="text-xs text-red-600 mb-1 font-medium">- Removed</div>
                  <pre className="text-sm font-mono text-red-800 whitespace-pre-wrap">{oldContent.split('\n').map((line: string, i: number) => (
                    <div key={i} className="flex">
                      <span className="text-red-400 mr-2">-</span>
                      <span>{line}</span>
                    </div>
                  ))}</pre>
                </div>
              )}
              {newContent && (
                <div className="bg-green-50 border-l-4 border-green-400 p-3">
                  <div className="text-xs text-green-600 mb-1 font-medium">+ Added</div>
                  <pre className="text-sm font-mono text-green-800 whitespace-pre-wrap">{newContent.split('\n').map((line: string, i: number) => (
                    <div key={i} className="flex">
                      <span className="text-green-400 mr-2">+</span>
                      <span>{line}</span>
                    </div>
                  ))}</pre>
                </div>
              )}
            </div>
          </div>
        </div>
      );
    });
  };

  if (isMultiEdit) {
    return (
      <ToolCard
        title="🔄 File Multi Edit"
        badge={{ text: `${replacements} replacements`, className: 'badge-info' }}
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
  }

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