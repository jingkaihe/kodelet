import React from 'react';
import { ToolResult, SkillMetadata } from '../../types';

interface SkillRendererProps {
  toolResult: ToolResult;
}

const SkillRenderer: React.FC<SkillRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SkillMetadata;
  if (!meta) return null;

  return (
    <div className="card bg-accent/10 border border-accent/20">
      <div className="card-body">
        <div className="flex items-center gap-2 mb-3">
          <h4 className="font-semibold text-accent">⚡ Skill Loaded</h4>
          <div className="badge badge-accent badge-sm">{meta.skillName}</div>
        </div>

        <div className="bg-base-200 p-4 rounded-lg border">
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium">Directory:</span>
            <code className="bg-base-300 px-2 py-1 rounded text-xs">{meta.directory}</code>
          </div>
        </div>
      </div>
    </div>
  );
};

export default SkillRenderer;
