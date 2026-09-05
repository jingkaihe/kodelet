import React from 'react';
import { ApplyPatchMetadata, ToolResult } from '../../types';
import ApplyPatchRenderer from './ApplyPatchRenderer';

interface FileWriteMetadata {
  filePath: string;
  unifiedDiff?: string;
}

interface FileWriteRendererProps {
  toolResult: ToolResult;
}

const FileWriteRenderer: React.FC<FileWriteRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileWriteMetadata;
  if (!meta) return null;

  const patchMetadata: ApplyPatchMetadata = {
    changes: meta.filePath
      ? [{ path: meta.filePath, operation: 'write', unifiedDiff: meta.unifiedDiff }]
      : [],
  };

  return <ApplyPatchRenderer toolResult={{ ...toolResult, metadata: patchMetadata }} />;
};

export default FileWriteRenderer;
