import React from 'react';
import { ToolResult, BashMetadata } from '../../types';
import { ReferenceTerminal, ReferenceToolNote } from './reference';
import { CopyButton } from './shared';

interface BashRendererProps {
  toolResult: ToolResult;
  toolInput?: string;
  isPartial?: boolean;
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

const BashRenderer: React.FC<BashRendererProps> = ({
  toolResult,
  toolInput,
  isPartial = false,
}) => {
  const meta = toolResult.metadata as BashMetadata;
  if (!meta) return null;

  const description = getDescriptionFromInput(toolInput);
  const hasOutput = !!meta.output?.trim();
  const exitCode = meta.exitCode ?? 0;
  const isFailure = !isPartial && (!toolResult.success || exitCode !== 0);
  const hasMeaningfulExitCode = toolResult.success || exitCode !== 0;
  const statusBadgeText = isPartial
    ? 'running'
    : hasMeaningfulExitCode
      ? `exit ${exitCode}`
      : 'failed';
  const emptyOutputText = isPartial
    ? 'Waiting for command output…'
    : isFailure
      ? 'Command failed without output.'
      : 'Command completed without output.';

  return (
    <div className="space-y-2">
      <div className="bash-tool-brief">
        <div className="min-w-0">
          {description ? (
            <div className="bash-tool-description">{description}</div>
          ) : (
            <div className="bash-tool-description is-muted">command output</div>
          )}
        </div>
        <div className="bash-tool-actions">
          <CopyButton className="bash-copy-command" content={meta.command} />
          <span
            className={
              isPartial
                ? 'bash-tool-badge'
                : isFailure
                  ? 'bash-tool-badge is-error'
                  : 'bash-tool-badge is-success'
            }
          >
            {statusBadgeText}
          </span>
        </div>
      </div>

      {!isPartial && !toolResult.success && toolResult.error ? (
        <ReferenceToolNote text={toolResult.error} />
      ) : null}

      {hasOutput ? (
        <ReferenceTerminal output={meta.output || ''} />
      ) : (
        <ReferenceToolNote text={emptyOutputText} />
      )}
    </div>
  );
};

export default BashRenderer;
