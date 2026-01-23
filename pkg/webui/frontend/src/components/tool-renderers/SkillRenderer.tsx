import React from 'react';
import { ToolResult, SkillMetadata } from '../../types';

interface SkillRendererProps {
  toolResult: ToolResult;
}

const SkillRenderer: React.FC<SkillRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SkillMetadata;
  if (!meta) return null;

  return (
    <div className="bg-kodelet-green/5 border border-kodelet-green/20 rounded p-3">
      <div className="flex items-center gap-2 mb-2">
        <h4 className="font-heading font-semibold text-sm text-kodelet-green">⚡ Skill Loaded</h4>
        <div className="px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20">
          {meta.skillName}
        </div>
      </div>

      <div className="bg-kodelet-light-gray/30 p-3 rounded border border-kodelet-mid-gray/20">
        <div className="flex items-center gap-2 text-xs">
          <span className="font-heading font-medium text-kodelet-mid-gray">Directory:</span>
          <code className="bg-kodelet-light px-2 py-1 rounded text-xs font-mono text-kodelet-dark">{meta.directory}</code>
        </div>
      </div>
    </div>
  );
};

export default SkillRenderer;
