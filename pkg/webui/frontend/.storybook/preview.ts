import type { Preview } from '@storybook/react-vite';
import '../src/styles/index.css';
import { initializeTheme } from '../src/theme';

const cleanupTheme = initializeTheme();
import.meta.hot?.dispose(cleanupTheme);

const preview: Preview = {
  parameters: {
    actions: { argTypesRegex: '^on[A-Z].*' },
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    layout: 'fullscreen',
  },
};

export default preview;
