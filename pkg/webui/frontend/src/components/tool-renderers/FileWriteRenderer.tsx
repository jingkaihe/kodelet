import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import { ToolCard, CopyButton, MetadataRow, Collapsible, CodeBlock } from './shared';
import { detectLanguageFromPath, formatFileSize } from './utils';

interface FileWriteMetadata extends FileMetadata {
  content?: string;
}

interface FileWriteRendererProps {
  toolResult: ToolResult;
}

const FileWriteRenderer: React.FC<FileWriteRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileWriteMetadata;
  if (!meta) return null;

  const language = meta.language || detectLanguageFromPath(meta.filePath);
  const sizeText = meta.size ? formatFileSize(meta.size) : '';

  return (
    <ToolCard
      title="📝 File Written"
      badge={{ text: 'Success', className: 'badge-success' }}
      actions={meta.content ? <CopyButton content={meta.content} /> : undefined}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="flex items-center gap-4">
          <MetadataRow label="Path" value={meta.filePath} monospace />
          {sizeText && <MetadataRow label="Size" value={sizeText} />}
          {language && <MetadataRow label="Language" value={language} />}
        </div>
      </div>

      {meta.content && (
        <Collapsible
          title="View Content"
          collapsed={true}
          badge={{ text: 'View Content', className: 'badge-info' }}
        >
          <CodeBlock 
            code={meta.content} 
            language={language} 
            showLineNumbers={true} 
            maxHeight={300} 
          />
        </Collapsible>
      )}
    </ToolCard>
  );
};

export default FileWriteRenderer;