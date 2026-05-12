import type { ComponentType } from 'react';
import type { ToolRenderProps } from '../../types';
import ApplyPatchRenderer from './ApplyPatchRenderer';
import BashRenderer from './BashRenderer';
import CodeExecutionRenderer from './CodeExecutionRenderer';
import CustomToolRenderer from './CustomToolRenderer';
import FileEditRenderer from './FileEditRenderer';
import FileReadRenderer from './FileReadRenderer';
import FileWriteRenderer from './FileWriteRenderer';
import GlobRenderer from './GlobRenderer';
import GrepRenderer from './GrepRenderer';
import MCPToolRenderer from './MCPToolRenderer';
import OpenAIWebSearchRenderer from './OpenAIWebSearchRenderer';
import ReadConversationRenderer from './ReadConversationRenderer';
import SkillRenderer from './SkillRenderer';
import SubagentRenderer from './SubagentRenderer';
import ThinkingRenderer from './ThinkingRenderer';
import ViewImageRenderer from './ViewImageRenderer';
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
  subagent: { component: SubagentRenderer },
  view_image: { component: ViewImageRenderer },
  skill: { component: SkillRenderer },
  openai_web_search: { component: OpenAIWebSearchRenderer, supportsFailureRendering: true },
  read_conversation: { component: ReadConversationRenderer },
  code_execution: { component: CodeExecutionRenderer, supportsFailureRendering: true },
  custom_tool: { component: CustomToolRenderer },
  mcp_tool: { component: MCPToolRenderer },
};

export const getToolRendererRegistration = (toolName: string): ToolRendererRegistration | undefined =>
  toolRendererRegistry[normalizeToolName(toolName)];
