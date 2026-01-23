import React from 'react';
import { ToolResult, SkillMetadata } from '../../types';
import { StatusBadge } from './shared';

interface SkillRendererProps {
  toolResult: ToolResult;
}

const SkillRenderer: React.FC<SkillRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SkillMetadata;
  if (!meta) return null;

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text={meta.skillName} variant="success" />
        <span className="text-kodelet-mid-gray">loaded</span>
      </div>
      <div className="text-xs font-mono text-kodelet-dark/70">{meta.directory}</div>
    </div>
  );
};

export default SkillRenderer;
