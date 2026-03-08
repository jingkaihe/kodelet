import React from 'react';
import { ToolResult, BashMetadata } from '../../types';
import {
  formatReferenceDuration,
  formatReferenceSize,
  ReferenceTerminal,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  ReferenceToolNote,
  TOOL_ICONS,
} from './reference';

interface BashRendererProps {
  toolResult: ToolResult;
}

const BashRenderer: React.FC<BashRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BashMetadata;
  if (!meta) return null;

  const hasOutput = meta.output && meta.output.trim();
  const exitCode = meta.exitCode || 0;
  const isSuccess = exitCode === 0;

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: `exit ${exitCode}`, variant: isSuccess ? 'success' : 'error' },
          {
            text: hasOutput ? formatReferenceSize(new TextEncoder().encode(meta.output || '').length) : 'no output',
            variant: 'neutral',
          },
        ]}
        subtitle={meta.command}
        title={`${TOOL_ICONS.bash} Shell Command`}
      />

      <ReferenceToolKVGrid
        items={[
          { label: 'Working dir', value: meta.workingDir, monospace: true },
          { label: 'Duration', value: formatReferenceDuration(meta.executionTime), monospace: true },
        ]}
      />

      {hasOutput ? (
        <ReferenceTerminal output={meta.output || ''} />
      ) : (
        <ReferenceToolNote text="Command completed without output." />
      )}
    </div>
  );
};

export default BashRenderer;
