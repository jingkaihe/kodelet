import type { Meta, StoryObj } from '@storybook/react-vite';
import ToolRenderer from '../ToolRenderer';
import type { ToolResult } from '../../types';
import {
  sampleBashToolResult,
  sampleFileReadToolResult,
} from '../../stories/fixtures';

interface ToolExample {
  title: string;
  input?: Record<string, unknown>;
  result: ToolResult;
}

const examples: ToolExample[] = [
  {
    title: 'Bash',
    input: {
      command: 'npm run test:run',
      description: 'Run frontend tests',
    },
    result: sampleBashToolResult,
  },
  {
    title: 'File read',
    input: {
      file_path: 'pkg/webui/frontend/src/components/chat/ChatComposer.tsx',
      offset: 1,
      line_limit: 80,
    },
    result: sampleFileReadToolResult,
  },
  {
    title: 'File write',
    result: {
      toolName: 'file_write',
      success: true,
      metadata: {
        filePath: 'pkg/webui/frontend/src/components/chat/ChatComposer.stories.tsx',
        language: 'tsx',
        size: 1840,
        content: 'export const ReadyToSend = {};\n',
      },
    },
  },
  {
    title: 'File edit',
    result: {
      toolName: 'file_edit',
      success: true,
      metadata: {
        filePath: 'pkg/webui/frontend/src/pages/ChatPage.tsx',
        edits: [
          {
            startLine: 20,
            endLine: 20,
            oldContent: 'import ChatSidebar from "../components/chat/ChatSidebar";',
            newContent:
              'import ChatComposer from "../components/chat/ChatComposer";\nimport ChatSidebar from "../components/chat/ChatSidebar";',
          },
        ],
      },
    },
  },
  {
    title: 'Apply patch',
    result: {
      toolName: 'apply_patch',
      success: true,
      metadata: {
        changes: [
          {
            path: 'pkg/webui/frontend/src/components/workspace/TerminalModal.tsx',
            operation: 'update',
            unifiedDiff:
              '@@ -1,3 +1,4 @@\n import React from "react";\n+import TerminalModalFrame from "./TerminalModalFrame";',
          },
        ],
      },
    },
  },
  {
    title: 'Search',
    result: {
      toolName: 'grep_tool',
      success: true,
      metadata: {
        pattern: 'ChatComposer',
        path: 'pkg/webui/frontend/src',
        include: '*.tsx',
        results: [
          {
            filePath: 'pkg/webui/frontend/src/pages/ChatPage.tsx',
            matches: [
              {
                lineNumber: 20,
                content: 'import ChatComposer from "../components/chat/ChatComposer";',
              },
              {
                lineNumber: 1927,
                content: '<ChatComposer',
              },
            ],
          },
        ],
      },
    },
  },
  {
    title: 'Glob',
    result: {
      toolName: 'glob_tool',
      success: true,
      metadata: {
        pattern: '**/*.stories.tsx',
        path: 'pkg/webui/frontend/src',
        files: [
          {
            path: 'components/chat/ChatComposer.stories.tsx',
            size: 3210,
            type: 'file',
            language: 'tsx',
          },
          {
            path: 'components/workspace/TerminalModalFrame.stories.tsx',
            size: 1550,
            type: 'file',
            language: 'tsx',
          },
        ],
      },
    },
  },
  {
    title: 'Web fetch',
    result: {
      toolName: 'web_fetch',
      success: true,
      metadata: {
        url: 'https://storybook.js.org/docs',
        contentType: 'text/html',
        processedType: 'markdown',
        size: 12480,
        content: '# Storybook docs\n\nStories capture component states as examples.',
      },
    },
  },
  {
    title: 'OpenAI web search',
    input: {
      type: 'search',
      query: 'Storybook React Vite component stories',
    },
    result: {
      toolName: 'openai_web_search',
      success: true,
      metadata: {
        action: 'search',
        status: 'completed',
        queries: ['Storybook React Vite component stories'],
        sources: ['https://storybook.js.org/docs/get-started/frameworks/react-vite'],
      },
    },
  },
  {
    title: 'View image',
    result: {
      toolName: 'view_image',
      success: true,
      metadata: {
        path: 'pkg/webui/frontend/storybook-screenshot.png',
        mimeType: 'image/png',
        detail: 'high',
        imageSize: {
          width: 1440,
          height: 900,
        },
      },
    },
  },
  {
    title: 'Read conversation',
    result: {
      toolName: 'read_conversation',
      success: true,
      metadata: {
        conversationID: '01HZSTORYBOOK',
        goal: 'Summarize extraction work',
        content:
          'The conversation extracted ChatComposer and NewChatContextDialog into testable components.',
      },
    },
  },
  {
    title: 'Extension tool',
    result: {
      toolName: 'design_snapshot',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'design',
        toolName: 'design_snapshot',
        executionTime: 0.42,
        output: '{"components":7,"stories":18}',
      },
    },
  },
  {
    title: 'Skill',
    result: {
      toolName: 'skill',
      success: true,
      metadata: {
        skillName: 'storybook-ui',
        directory: '.kodelet/skills/storybook-ui',
      },
    },
  },
];

const ToolRendererCatalog = () => (
  <div className="min-h-screen bg-[rgba(244,239,229,0.78)] px-5 py-6">
    <div className="mx-auto grid max-w-6xl gap-4 lg:grid-cols-2">
      {examples.map((example) => (
        <section className="surface-panel rounded-2xl p-4" key={example.title}>
          <h3 className="mb-3 text-sm font-semibold">{example.title}</h3>
          <ToolRenderer
            toolInput={example.input ? JSON.stringify(example.input) : undefined}
            toolResult={example.result}
          />
        </section>
      ))}
    </div>
  </div>
);

const meta = {
  title: 'Tools/RendererCatalog',
  component: ToolRendererCatalog,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof ToolRendererCatalog>;

export default meta;

type Story = StoryObj<typeof meta>;

export const AllRenderers: Story = {};
