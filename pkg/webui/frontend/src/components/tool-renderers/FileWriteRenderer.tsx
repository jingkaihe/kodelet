import React, { useState } from 'react';
import { ToolResult, FileMetadata } from '../../types';
import { CopyButton, StatusBadge } from './shared';
import { detectLanguageFromPath, formatFileSize } from './utils';

interface FileWriteMetadata extends FileMetadata {
  content?: string;
}

interface FileWriteRendererProps {
  toolResult: ToolResult;
}

const FileWriteRenderer: React.FC<FileWriteRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileWriteMetadata;
  const [showContent, setShowContent] = useState(false);
  if (!meta) return null;

  const language = meta.language || detectLanguageFromPath(meta.filePath);
  const sizeText = meta.size ? formatFileSize(meta.size) : '';
  const lines = meta.content?.split('\n') || [];

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
          <span className="font-medium">{meta.filePath}</span>
          <StatusBadge text="Written" variant="success" />
          {sizeText && <span className="text-kodelet-mid-gray">{sizeText}</span>}
          {language && <span className="text-kodelet-mid-gray">{language}</span>}
        </div>
        {meta.content && <CopyButton content={meta.content} />}
      </div>

      {meta.content && (
        <>
          {!showContent ? (
            <button
              onClick={() => setShowContent(true)}
              className="text-xs text-kodelet-blue hover:underline"
            >
              Show content ({lines.length} lines)
            </button>
          ) : (
            <div
              className="bg-kodelet-light text-xs font-mono rounded border border-kodelet-light-gray"
              style={{ maxHeight: '300px', overflowY: 'auto' }}
            >
              <div className="p-3">
                {lines.slice(0, 50).map((line, index) => (
                  <div key={index} className="flex">
                    <span className="text-kodelet-mid-gray min-w-[3rem] text-right pr-2 select-none">{index + 1}</span>
                    <span className="text-kodelet-dark">{line || '\u00A0'}</span>
                  </div>
                ))}
                {lines.length > 50 && (
                  <div className="text-kodelet-mid-gray mt-2">... and {lines.length - 50} more lines</div>
                )}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default FileWriteRenderer;