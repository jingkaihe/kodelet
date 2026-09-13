/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        'heading': ['IBM Plex Sans', 'Helvetica Neue', 'sans-serif'],
        'body': ['Crimson Pro', 'Georgia', 'serif'],
        'mono': ['JetBrains Mono', 'SFMono-Regular', 'Consolas', 'monospace'],
      },
      colors: {
        // Keep neutral colors in sync with styles/foundation.css.
        'kodelet': {
          'dark': '#3c3836',
          'light': '#faf8ef',
          'mid-gray': '#d5c4a1',
          'light-gray': '#ebdbb2',
          'orange': '#d97757',
          'blue': '#6a9bcc',
          'green': '#788c5d',
        },
      },
    },
  },
  plugins: [require('daisyui')],
  daisyui: {
    themes: [
      {
        kodelet: {
          'primary': '#d97757',
          'secondary': '#6a9bcc',
          'accent': '#788c5d',
          'neutral': '#3c3836',
          'base-100': '#faf8ef',
          'base-200': '#f8f5e9',
          'base-300': '#ebdbb2',
          'base-content': '#3c3836',
          'info': '#6a9bcc',
          'success': '#788c5d',
          'warning': '#d97757',
          'error': '#d97757',
        },
      },
    ],
    base: true,
    styled: true,
    utils: true,
  },
};
