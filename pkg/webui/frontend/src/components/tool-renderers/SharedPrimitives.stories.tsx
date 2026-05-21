import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import {
  CodeBlock,
  Collapsible,
  CopyButton,
  ExternalLink,
  MetadataRow,
  StatusBadge,
  ToolCard,
} from './shared';

const SharedPrimitiveGallery = () => (
  <div className="min-h-screen bg-[rgba(244,239,229,0.78)] px-5 py-6">
    <div className="mx-auto grid max-w-5xl gap-4 lg:grid-cols-2">
      <ToolCard
        actions={<CopyButton content="npm run test:run" />}
        badge={{ text: 'success' }}
        title="Tool card"
      >
        <div className="space-y-2">
          <MetadataRow label="Runtime" value="node" />
          <MetadataRow label="Path" monospace value="pkg/webui/frontend" />
          <div className="flex flex-wrap gap-2">
            <StatusBadge text="done" variant="success" />
            <StatusBadge text="warning" variant="warning" />
            <StatusBadge text="info" variant="info" />
            <StatusBadge text="failed" variant="error" />
          </div>
        </div>
      </ToolCard>

      <ToolCard title="Code block" badge={{ text: 'tsx' }}>
        <CodeBlock
          code={'const story = "component state";\nexpect(story).toBeTruthy();'}
          language="tsx"
          maxHeight={180}
        />
      </ToolCard>

      <ToolCard title="Collapsible details">
        <Collapsible badge={{ text: '3 files' }} title="Changed files">
          <CodeBlock
            code={'ChatComposer.tsx\nChatComposer.stories.tsx\nChatComposer.test.tsx'}
            showLineNumbers={false}
          />
        </Collapsible>
      </ToolCard>

      <ToolCard title="External link">
        <div className="space-y-2 text-sm">
          <ExternalLink href="https://storybook.js.org/docs">
            Storybook documentation
          </ExternalLink>
          <div>
            <ExternalLink href="javascript:alert(1)">Blocked unsafe URL</ExternalLink>
          </div>
        </div>
      </ToolCard>
    </div>
  </div>
);

const meta = {
  title: 'Tools/SharedPrimitives',
  component: SharedPrimitiveGallery,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    onClick: fn(),
  },
} satisfies Meta<typeof SharedPrimitiveGallery>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Gallery: Story = {};
