import type { Meta, StoryObj } from '@storybook/react-vite';
import { CopyButton, ExternalLink } from './shared';

const SharedPrimitiveGallery = () => (
  <div className="min-h-screen bg-[rgba(244,239,229,0.78)] px-5 py-6">
    <div className="mx-auto grid max-w-5xl gap-4 lg:grid-cols-2">
      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">Copy button</h3>
        <CopyButton content="npm run test:run" />
      </section>

      <section className="surface-panel rounded-2xl p-4">
        <h3 className="mb-3 text-sm font-semibold">External link</h3>
        <div className="space-y-2 text-sm">
          <ExternalLink href="https://storybook.js.org/docs">Storybook documentation</ExternalLink>
          <div>
            <ExternalLink href="javascript:alert(1)">Blocked unsafe URL</ExternalLink>
          </div>
        </div>
      </section>
    </div>
  </div>
);

const meta = {
  title: 'Tools/SharedPrimitives',
  component: SharedPrimitiveGallery,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof SharedPrimitiveGallery>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Gallery: Story = {};
