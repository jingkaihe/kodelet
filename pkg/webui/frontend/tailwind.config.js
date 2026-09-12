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
        // Kodelet Brand Colors
        'kodelet': {
          'dark': '#141413',
          'light': '#faf9f5',
          'mid-gray': '#b0aea5',
          'light-gray': '#e8e6dc',
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
          'neutral': '#141413',
          'base-100': '#faf9f5',
          'base-200': '#e8e6dc',
          'base-300': '#b0aea5',
          'base-content': '#141413',
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
