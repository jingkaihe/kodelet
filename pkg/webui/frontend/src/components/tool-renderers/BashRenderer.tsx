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

  const hasOutput = !!meta.output?.trim();
  const exitCode = meta.exitCode ?? 0;
  const isFailure = !toolResult.success || exitCode !== 0;
  const hasMeaningfulExitCode = toolResult.success || exitCode !== 0;
  const statusBadgeText = hasMeaningfulExitCode ? `exit ${exitCode}` : 'failed';
  const emptyOutputText = isFailure
    ? 'Command failed without output.'
    : 'Command completed without output.';

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: statusBadgeText, variant: isFailure ? 'error' : 'success' },
          {
            text: hasOutput ? formatReferenceSize(new TextEncoder().encode(meta.output || '').length) : 'no output',
            variant: 'neutral',
          },
        ]}
        subtitle={meta.command}
        title={`${TOOL_ICONS.bash} Shell Command`}
      />

      {!toolResult.success && toolResult.error ? (
        <ReferenceToolNote text={toolResult.error} />
      ) : null}

      <ReferenceToolKVGrid
        items={[
          { label: 'Working dir', value: meta.workingDir, monospace: true },
          { label: 'Duration', value: formatReferenceDuration(meta.executionTime), monospace: true },
        ]}
      />

      {hasOutput ? (
        <ReferenceTerminal output={meta.output || ''} />
      ) : (
        <ReferenceToolNote text={emptyOutputText} />
      )}
    </div>
  );
};

export default BashRenderer;
