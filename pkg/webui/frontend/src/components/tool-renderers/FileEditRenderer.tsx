import React from 'react';
import { ToolResult } from '../../types';
import { ToolCard, MetadataRow, Collapsible, detectLanguageFromPath } from './shared';

interface FileEditRendererProps {
  toolResult: ToolResult;
}

const FileEditRenderer: React.FC<FileEditRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata;
  if (!meta) return null;

  const language = meta.language || detectLanguageFromPath(meta.filePath);
  const edits = meta.edits || [];
  const isMultiEdit = toolResult.toolName === 'file_multi_edit';
  const replacements = meta.occurrence || meta.replacements || 0;

  const renderEdits = (edits: any[]) => {
    return edits.map((edit: any, index: number) => {
      const oldText = edit.oldText || '';
      const newText = edit.newText || '';

      return (
        <div key={index} className="mb-4">
          <h5 className="text-sm font-medium mb-2">Edit {index + 1}: Lines {edit.startLine}-{edit.endLine}</h5>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <div className="text-xs text-red-600 mb-1">- Removed</div>
              <div className="mockup-code bg-red-50 border-red-200">
                <pre><code className={`language-${language}`}>{oldText}</code></pre>
              </div>
            </div>
            <div>
              <div className="text-xs text-green-600 mb-1">+ Added</div>
              <div className="mockup-code bg-green-50 border-green-200">
                <pre><code className={`language-${language}`}>{newText}</code></pre>
              </div>
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
        <div className="text-xs text-base-content/60 font-mono">
          <div className="flex items-center gap-4">
            <MetadataRow label="Path" value={meta.filePath} monospace />
            <MetadataRow label="Pattern" value={meta.oldText || 'N/A'} monospace />
          </div>
        </div>
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