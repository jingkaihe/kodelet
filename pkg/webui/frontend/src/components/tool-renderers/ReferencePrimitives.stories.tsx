import type { Meta, StoryObj } from '@storybook/react-vite';
import {
  ReferenceCodeBlock,
  ReferenceCodeList,
  ReferenceDiffBlock,
  ReferenceFileList,
  ReferenceTerminal,
  ReferenceToolKVGrid,
  ReferenceToolNote,
  parseUnifiedDiff,
} from './reference';

const diffLines = parseUnifiedDiff(
  [
    '--- a/pkg/webui/frontend/src/components/chat/ChatTranscript.tsx',
    '+++ b/pkg/webui/frontend/src/components/chat/ChatTranscript.tsx',
    '@@ -1,5 +1,6 @@',
    ' import React from "react";',
    '+import ChatMessageFrame from "./ChatMessageFrame";',
    ' import ToolRenderer from "../ToolRenderer";',
    '-const renderFrame = () => null;',
    '+const renderFrame = () => <ChatMessageFrame role="assistant" />;',
  ].join('\n')
);

const ReferencePrimitiveGallery = () => (
  <div className="min-h-screen bg-[rgba(244,239,229,0.78)] px-5 py-6">
    <div className="mx-auto grid max-w-6xl gap-4 lg:grid-cols-2">
      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Key/value grid</h3>
        <ReferenceToolKVGrid
          items={[
            { label: 'Language', value: 'tsx' },
            { label: 'Offset', value: 42, monospace: true },
            { label: 'Empty values are hidden', value: '' },
          ]}
        />
        <div className="mt-3">
          <ReferenceToolNote text="Use offset=83 to continue reading this file." />
        </div>
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Code block</h3>
        <ReferenceCodeBlock
          content={'export const Story = {};\nrender(<Component />);'}
          language="tsx"
        />
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Diff block</h3>
        <ReferenceDiffBlock lines={diffLines} />
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Terminal output</h3>
        <ReferenceTerminal output={'npm run storybook:build\n---\nStorybook build completed successfully'} />
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">File list</h3>
        <ReferenceFileList
          items={[
            { path: 'components/chat/ChatComposer.tsx', meta: 'tsx · 8 KB' },
            { path: 'components/chat/ChatComposer.stories.tsx', meta: 'tsx · 3 KB' },
            { path: 'components/chat/ChatComposer.test.tsx', meta: 'tsx · 2 KB' },
          ]}
        />
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Code list</h3>
        <ReferenceCodeList
          items={[
            'ChatComposer',
            'NewChatContextDialog',
            'TerminalModalFrame',
            'ArcadeGamesModal',
          ]}
        />
      </section>
    </div>
  </div>
);

const meta = {
  title: 'Tools/ReferencePrimitives',
  component: ReferencePrimitiveGallery,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof ReferencePrimitiveGallery>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Gallery: Story = {};
