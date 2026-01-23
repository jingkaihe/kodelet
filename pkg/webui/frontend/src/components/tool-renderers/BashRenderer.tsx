import React from 'react';
import { ToolResult, BashMetadata } from '../../types';
import { ToolCard, CopyButton, MetadataRow, Collapsible } from './shared';
import { formatDuration } from './utils';

interface BashRendererProps {
  toolResult: ToolResult;
}

const BashRenderer: React.FC<BashRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BashMetadata;
  if (!meta) return null;

  const isBackground = meta.pid !== undefined;
  const hasOutput = meta.output && meta.output.trim();
  const exitCode = meta.exitCode || 0;
  const isSuccess = exitCode === 0;

  if (isBackground) {
    return (
      <ToolCard
        title="Background Process"
        badge={{ text: `PID: ${meta.pid}`, className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
      >
        <div className="text-xs text-kodelet-dark/60 font-mono">
          <div className="space-y-1">
            <MetadataRow label="Command" value={meta.command} monospace />
            <MetadataRow label="Log File" value={meta.logPath || meta.logFile || 'N/A'} monospace />
            {meta.startTime && <MetadataRow label="Started" value={new Date(meta.startTime).toLocaleString()} />}
          </div>
        </div>
      </ToolCard>
    );
  }

  const createTerminalOutput = (output: string) => {
    const ansiToHtml = (text: string) => {
      const ESC = '\u001b';
      return text
        .replace(new RegExp(`${ESC}\\[31m`, 'g'), '<span class="text-red-500">')
        .replace(new RegExp(`${ESC}\\[32m`, 'g'), '<span class="text-green-500">')
        .replace(new RegExp(`${ESC}\\[33m`, 'g'), '<span class="text-yellow-500">')
        .replace(new RegExp(`${ESC}\\[34m`, 'g'), '<span class="text-blue-500">')
        .replace(new RegExp(`${ESC}\\[35m`, 'g'), '<span class="text-purple-500">')
        .replace(new RegExp(`${ESC}\\[36m`, 'g'), '<span class="text-cyan-500">')
        .replace(new RegExp(`${ESC}\\[37m`, 'g'), '<span class="text-gray-500">')
        .replace(new RegExp(`${ESC}\\[0m`, 'g'), '</span>')
        .replace(new RegExp(`${ESC}\\[\\d+m`, 'g'), '');
    };

    const escapeHtml = (text: string) => {
      const div = document.createElement('div');
      div.textContent = text;
      return div.innerHTML;
    };

    return (
      <div className="bg-kodelet-dark text-kodelet-green text-sm max-h-96 overflow-y-auto rounded-lg p-4 font-mono">
        <pre dangerouslySetInnerHTML={{ __html: ansiToHtml(escapeHtml(output)) }} />
      </div>
    );
  };

  return (
    <ToolCard
      title="Command Execution"
      badge={{
        text: `Exit ${exitCode}`,
        className: isSuccess 
          ? 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20'
          : 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-orange/10 text-kodelet-orange border border-kodelet-orange/20'
      }}
      actions={hasOutput ? <CopyButton content={meta.output || ''} /> : undefined}
    >
      <div className="text-xs text-kodelet-dark/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Command" value={meta.command} monospace />
          {meta.workingDir && <MetadataRow label="Directory" value={meta.workingDir} monospace />}
          {meta.executionTime && <MetadataRow label="Duration" value={formatDuration(meta.executionTime)} />}
        </div>
      </div>

      {hasOutput ? (
        <Collapsible
          title="Output"
          collapsed={false}
          badge={{ text: 'Terminal', className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-dark/10 text-kodelet-dark border border-kodelet-dark/20' }}
        >
          {createTerminalOutput(meta.output || '')}
        </Collapsible>
      ) : (
        <div className="text-sm text-kodelet-dark/50 font-body">No output</div>
      )}
    </ToolCard>
  );
};

export default BashRenderer;