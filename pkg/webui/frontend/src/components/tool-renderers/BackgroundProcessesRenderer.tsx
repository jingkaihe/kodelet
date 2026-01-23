import React from 'react';
import { ToolResult, BackgroundProcessMetadata, BackgroundProcess } from '../../types';
import { ToolCard, Collapsible } from './shared';
import { escapeHtml } from './utils';

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
      const statusBadgeClass = process.status === 'running' 
        ? 'bg-kodelet-green/10 text-kodelet-green border-kodelet-green/20'
        : 'bg-kodelet-orange/10 text-kodelet-orange border-kodelet-orange/20';

      return (
        <div key={index} className="flex items-center justify-between p-2 hover:bg-kodelet-light-gray/20 rounded">
          <div className="flex-1">
            <div className="text-sm font-mono text-kodelet-dark">
              {escapeHtml(process.command || 'Unknown')}
            </div>
            <div className="text-xs text-kodelet-mid-gray font-body">
              PID: {process.pid || 'Unknown'}
            </div>
          </div>
          <div className={`px-1.5 py-0.5 rounded text-xs font-heading font-medium border ${statusBadgeClass}`}>
            {process.status || 'Unknown'}
          </div>
        </div>
      );
    });

    return (
      <Collapsible
        title="Processes"
        collapsed={false}
        badge={{ text: `${processes.length} processes`, className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
      >
        <div>{processContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="Background Processes"
      badge={{ text: `${processCount} processes`, className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
    >
      {processes.length > 0 ? (
        renderProcessList(processes)
      ) : (
        <div className="text-sm font-body text-kodelet-mid-gray">No background processes</div>
      )}
    </ToolCard>
  );
};

export default BackgroundProcessesRenderer;