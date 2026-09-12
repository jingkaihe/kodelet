import type React from 'react';
import type { SkillMetadata, ToolResult } from '../../types';

interface SkillRendererProps {
  toolResult: ToolResult;
}

const SkillRenderer: React.FC<SkillRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SkillMetadata;
  if (!meta) return null;

  return (
    <div className="skill-tool-detail">
      <div className="skill-tool-status">
        <span className="skill-tool-name">{meta.skillName.toLowerCase()}</span>
        <span className="skill-tool-loaded">loaded</span>
      </div>
      <div className="skill-tool-path">{meta.directory}</div>
    </div>
  );
};

export default SkillRenderer;
