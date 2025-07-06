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
        title="🔄 Background Process"
        badge={{ text: `PID: ${meta.pid}`, className: 'badge-info' }}
      >
        <div className="text-xs text-base-content/60 font-mono">
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
    // Convert ANSI codes to HTML (basic implementation)
    const ansiToHtml = (text: string) => {
      // Use proper escape sequences to avoid ESLint no-control-regex warnings
      const ESC = '\u001b';
      return text
        .replace(new RegExp(`${ESC}\\[31m`, 'g'), '<span class="text-red-500">')    // Red
        .replace(new RegExp(`${ESC}\\[32m`, 'g'), '<span class="text-green-500">')  // Green
        .replace(new RegExp(`${ESC}\\[33m`, 'g'), '<span class="text-yellow-500">') // Yellow
        .replace(new RegExp(`${ESC}\\[34m`, 'g'), '<span class="text-blue-500">')   // Blue
        .replace(new RegExp(`${ESC}\\[35m`, 'g'), '<span class="text-purple-500">') // Magenta
        .replace(new RegExp(`${ESC}\\[36m`, 'g'), '<span class="text-cyan-500">')   // Cyan
        .replace(new RegExp(`${ESC}\\[37m`, 'g'), '<span class="text-gray-500">')   // White
        .replace(new RegExp(`${ESC}\\[0m`, 'g'), '</span>')                        // Reset
        .replace(new RegExp(`${ESC}\\[\\d+m`, 'g'), '');                            // Remove other codes
    };

    const escapeHtml = (text: string) => {
      const div = document.createElement('div');
      div.textContent = text;
      return div.innerHTML;
    };

    return (
      <div className="mockup-code bg-gray-900 text-green-400 text-sm max-h-96 overflow-y-auto">
        <pre dangerouslySetInnerHTML={{ __html: ansiToHtml(escapeHtml(output)) }} />
      </div>
    );
  };

  return (
    <ToolCard
      title="🖥️ Command Execution"
      badge={{
        text: `Exit Code: ${exitCode}`,
        className: isSuccess ? 'badge-success' : 'badge-error'
      }}
      actions={hasOutput ? <CopyButton content={meta.output || ''} /> : undefined}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Command" value={meta.command} monospace />
          {meta.workingDir && <MetadataRow label="Directory" value={meta.workingDir} monospace />}
          {meta.executionTime && <MetadataRow label="Duration" value={formatDuration(meta.executionTime)} />}
        </div>
      </div>

      {hasOutput ? (
        <Collapsible
          title="Command Output"
          collapsed={false}
          badge={{ text: 'View Output', className: 'badge-info' }}
        >
          {createTerminalOutput(meta.output || '')}
        </Collapsible>
      ) : (
        <div className="text-sm text-base-content/60">No output</div>
      )}
    </ToolCard>
  );
};

export default BashRenderer;