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
        <span className="tool-meta-label normal-case tracking-normal">loaded</span>
      </div>
      <div className="tool-inline-code text-xs">{meta.directory}</div>
    </div>
  );
};

export default SkillRenderer;
