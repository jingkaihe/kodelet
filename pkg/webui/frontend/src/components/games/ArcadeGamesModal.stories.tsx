import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import ArcadeGamesModal from './ArcadeGamesModal';

const meta = {
  title: 'Games/ArcadeGamesModal',
  component: ArcadeGamesModal,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    selectedGame: null,
    onBackToGames: fn(),
    onClose: fn(),
    onSelectGame: fn(),
  },
} satisfies Meta<typeof ArcadeGamesModal>;

export default meta;

type Story = StoryObj<typeof meta>;

export const GamePicker: Story = {};

export const PongCanvas: Story = {
  args: {
    selectedGame: 'pong',
  },
};

export const TetrisCanvas: Story = {
  args: {
    selectedGame: 'tetris',
  },
};
