import React from 'react';
import { ToolResult, BackgroundProcessMetadata, BackgroundProcess } from '../../types';
import { StatusBadge } from './shared';

interface BackgroundProcessesRendererProps {
  toolResult: ToolResult;
}

const BackgroundProcessesRenderer: React.FC<BackgroundProcessesRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BackgroundProcessMetadata;
  if (!meta) return null;

  const processes = meta.processes || [];

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text={`${processes.length} processes`} variant="info" />
      </div>

      {processes.length > 0 ? (
        <div className="space-y-1 text-xs">
          {processes.map((process: BackgroundProcess, index: number) => (
            <div key={index} className="flex items-center justify-between">
              <div className="flex items-center gap-2 font-mono">
                <span className="text-kodelet-mid-gray">PID {process.pid}</span>
                <span className="text-kodelet-dark">{process.command || 'Unknown'}</span>
              </div>
              <StatusBadge
                text={process.status || 'unknown'}
                variant={process.status === 'running' ? 'success' : 'warning'}
              />
            </div>
          ))}
        </div>
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No background processes</div>
      )}
    </div>
  );
};

export default BackgroundProcessesRenderer;