import React from 'react';
import { ToolResult, BashMetadata } from '../../types';
import { CopyButton, StatusBadge } from './shared';
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

  if (isBackground) {
    return (
      <div className="space-y-2">
        <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
          <code className="font-medium">{meta.command}</code>
          <StatusBadge text={`PID: ${meta.pid}`} variant="info" />
        </div>
        <div className="text-xs text-kodelet-mid-gray">
          Log: {meta.logPath || meta.logFile || 'N/A'}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
          <code className="font-medium">{meta.command}</code>
          <StatusBadge text={`Exit ${exitCode}`} variant={isSuccess ? 'success' : 'error'} />
          {meta.executionTime && <span className="text-kodelet-mid-gray">{formatDuration(meta.executionTime)}</span>}
        </div>
        {hasOutput && <CopyButton content={meta.output || ''} />}
      </div>

      {hasOutput ? (
        <div className="bg-kodelet-dark text-kodelet-green text-xs max-h-64 overflow-y-auto rounded p-3 font-mono">
          <pre dangerouslySetInnerHTML={{ __html: ansiToHtml(escapeHtml(meta.output || '')) }} />
        </div>
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No output</div>
      )}
    </div>
  );
};

export default BashRenderer;