import React from 'react';
import { ToolResult, BashMetadata } from '../../types';
import {
  formatReferenceDuration,
  formatReferenceSize,
  ReferenceTerminal,
  ReferenceToolKVGrid,
  ReferenceToolNote,
} from './reference';

interface BashRendererProps {
  toolResult: ToolResult;
  toolInput?: string;
}

const getDescriptionFromInput = (toolInput?: string): string | undefined => {
  if (!toolInput) {
    return undefined;
  }

  try {
    const parsed = JSON.parse(toolInput) as Record<string, unknown>;
    const description = parsed.description;
    return typeof description === 'string' && description.trim().length > 0
      ? description.trim()
      : undefined;
  } catch {
    return undefined;
  }
};

const BashRenderer: React.FC<BashRendererProps> = ({ toolResult, toolInput }) => {
  const meta = toolResult.metadata as BashMetadata;
  if (!meta) return null;

  const description = getDescriptionFromInput(toolInput);
  const hasOutput = !!meta.output?.trim();
  const exitCode = meta.exitCode ?? 0;
  const isFailure = !toolResult.success || exitCode !== 0;
  const hasMeaningfulExitCode = toolResult.success || exitCode !== 0;
  const statusBadgeText = hasMeaningfulExitCode ? `exit ${exitCode}` : 'failed';
  const emptyOutputText = isFailure
    ? 'Command failed without output.'
    : 'Command completed without output.';
  const outputSize = hasOutput
    ? formatReferenceSize(new TextEncoder().encode(meta.output || '').length)
    : 'no output';

  return (
    <div className="space-y-2">
      <div className="bash-tool-brief">
        <div className="min-w-0">
          <div className="bash-tool-title">shell command</div>
          {description ? <div className="bash-tool-description">{description}</div> : null}
        </div>
        <div className="bash-tool-badges">
          <span className={isFailure ? 'bash-tool-badge is-error' : 'bash-tool-badge is-success'}>
            {statusBadgeText}
          </span>
          <span className="bash-tool-badge is-neutral">{outputSize}</span>
        </div>
      </div>

      <div className="bash-command-line" title={meta.command}>{meta.command}</div>

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
