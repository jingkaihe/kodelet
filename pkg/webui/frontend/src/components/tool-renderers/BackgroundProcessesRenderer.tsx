import React from 'react';
import { ToolResult, BackgroundProcessMetadata, BackgroundProcess } from '../../types';
import { ToolCard, Collapsible, escapeHtml } from './shared';

interface BackgroundProcessesRendererProps {
  toolResult: ToolResult;
}

const BackgroundProcessesRenderer: React.FC<BackgroundProcessesRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BackgroundProcessMetadata;
  if (!meta) return null;

  const processes = meta.processes || [];
  const processCount = meta.processCount || processes.length;

  const renderProcessList = (processes: BackgroundProcess[]) => {
    const processContent = processes.map((process, index) => {
      const statusIcon = process.status === 'running' ? '🟢' : '🔴';
      const statusClass = process.status === 'running' ? 'text-green-600' : 'text-red-600';

      return (
        <div key={index} className="flex items-center justify-between p-2 hover:bg-base-100 rounded">
          <div className="flex items-center gap-3">
            <span aria-label={process.status}>{statusIcon}</span>
            <div>
              <div className="text-sm font-mono">
                {escapeHtml(process.command || 'Unknown')}
              </div>
              <div className="text-xs text-base-content/60">
                PID: {process.pid || 'Unknown'}
              </div>
            </div>
          </div>
          <div className={`text-xs ${statusClass}`}>{process.status || 'Unknown'}</div>
        </div>
      );
    });

    return (
      <Collapsible
        title="Processes"
        collapsed={false}
        badge={{ text: `${processes.length} processes`, className: 'badge-info' }}
      >
        <div>{processContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="⚙️ Background Processes"
      badge={{ text: `${processCount} processes`, className: 'badge-info' }}
    >
      {processes.length > 0 ? (
        renderProcessList(processes)
      ) : (
        <div className="text-sm text-base-content/60">No background processes</div>
      )}
    </ToolCard>
  );
};

export default BackgroundProcessesRenderer;