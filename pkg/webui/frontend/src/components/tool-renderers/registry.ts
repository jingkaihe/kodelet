import type { ComponentType } from 'react';
import type { ToolRenderProps, ToolResult } from '../../types';
import ApplyPatchRenderer from './ApplyPatchRenderer';
import BashRenderer from './BashRenderer';
import ExtensionToolRenderer from './ExtensionToolRenderer';
import FileEditRenderer from './FileEditRenderer';
import FileReadRenderer from './FileReadRenderer';
import FileWriteRenderer from './FileWriteRenderer';
import GlobRenderer from './GlobRenderer';
import GrepRenderer from './GrepRenderer';
import OpenAIWebSearchRenderer from './OpenAIWebSearchRenderer';
import ReadConversationRenderer from './ReadConversationRenderer';
import SkillRenderer from './SkillRenderer';
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
  apply_patch: { component: ApplyPatchRenderer, supportsFailureRendering: true },
  bash: { component: BashRenderer, supportsFailureRendering: true },
  grep_tool: { component: GrepRenderer },
  glob_tool: { component: GlobRenderer },
  web_fetch: { component: WebFetchRenderer },
  thinking: { component: ThinkingRenderer },
  view_image: { component: ViewImageRenderer },
  skill: { component: SkillRenderer },
  openai_web_search: { component: OpenAIWebSearchRenderer, supportsFailureRendering: true },
  read_conversation: { component: ReadConversationRenderer },
  extension_tool: { component: ExtensionToolRenderer },
};

export const getToolRendererRegistration = (toolResult: ToolResult): ToolRendererRegistration | undefined => {
  if (toolResult.metadataType) {
    const metadataRegistration = toolRendererRegistry[normalizeToolName(toolResult.metadataType)];
    if (metadataRegistration) {
      return metadataRegistration;
    }
  }
  return toolRendererRegistry[normalizeToolName(toolResult.toolName)];
};
