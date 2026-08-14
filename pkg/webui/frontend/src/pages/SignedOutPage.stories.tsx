import type { Meta, StoryObj } from '@storybook/react-vite';
import SignedOutPage from './SignedOutPage';

const meta = {
  title: 'Authentication/SignedOutPage',
  component: SignedOutPage,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof SignedOutPage>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Default: Story = {};
