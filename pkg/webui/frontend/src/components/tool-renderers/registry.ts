import type { ComponentType } from 'react';
import type { ToolRenderProps } from '../../types';
import ApplyPatchRenderer from './ApplyPatchRenderer';
import BashRenderer from './BashRenderer';
import FileEditRenderer from './FileEditRenderer';
import FileReadRenderer from './FileReadRenderer';
import FileWriteRenderer from './FileWriteRenderer';
import GlobRenderer from './GlobRenderer';
import GrepRenderer from './GrepRenderer';
import ImageRecognitionRenderer from './ImageRecognitionRenderer';
import SkillRenderer from './SkillRenderer';
import SubagentRenderer from './SubagentRenderer';
import ThinkingRenderer from './ThinkingRenderer';
import TodoRenderer from './TodoRenderer';
import WebFetchRenderer from './WebFetchRenderer';
import { normalizeToolName } from './reference';

export interface ToolRendererRegistration {
  component: ComponentType<ToolRenderProps>;
  supportsFailureRendering?: boolean;
}

const toolRendererRegistry: Record<string, ToolRendererRegistration> = {
  file_read: { component: FileReadRenderer },
  file_write: { component: FileWriteRenderer },
  file_edit: { component: FileEditRenderer },
  apply_patch: { component: ApplyPatchRenderer },
  bash: { component: BashRenderer, supportsFailureRendering: true },
  grep_tool: { component: GrepRenderer },
  glob_tool: { component: GlobRenderer },
  web_fetch: { component: WebFetchRenderer },
  thinking: { component: ThinkingRenderer },
  todo_read: { component: TodoRenderer },
  todo_write: { component: TodoRenderer },
  subagent: { component: SubagentRenderer },
  image_recognition: { component: ImageRecognitionRenderer },
  skill: { component: SkillRenderer },
};

export const getToolRendererRegistration = (toolName: string): ToolRendererRegistration | undefined =>
  toolRendererRegistry[normalizeToolName(toolName)];
