import React from 'react';
import { ApplyPatchMetadata, ToolResult } from '../../types';
import ApplyPatchRenderer from './ApplyPatchRenderer';

interface FileEditMetadata {
  filePath: string;
  unifiedDiff?: string;
}

interface FileEditRendererProps {
  toolResult: ToolResult;
}

const FileEditRenderer: React.FC<FileEditRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileEditMetadata;
  if (!meta) return null;

  const patchMetadata: ApplyPatchMetadata = {
    changes: meta.filePath
      ? [{ path: meta.filePath, operation: 'update', unifiedDiff: meta.unifiedDiff }]
      : [],
  };

  return <ApplyPatchRenderer toolResult={{ ...toolResult, metadata: patchMetadata }} />;
};

export default FileEditRenderer;
