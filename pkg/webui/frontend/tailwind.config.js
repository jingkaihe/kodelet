/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        'heading': ['Poppins', 'Arial', 'sans-serif'],
        'body': ['Lora', 'Georgia', 'serif'],
        'mono': ['Monaco', 'Menlo', 'Ubuntu Mono', 'monospace'],
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
      animation: {
        'fade-in': 'fadeIn 0.6s ease-out',
        'slide-up': 'slideUp 0.5s ease-out',
        'slide-in-right': 'slideInRight 0.4s ease-out',
        'float': 'float 3s ease-in-out infinite',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(20px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' },
        },
        float: {
          '0%, 100%': { transform: 'translateY(0px)' },
          '50%': { transform: 'translateY(-10px)' },
        },
      },
    },
  },
  plugins: [
    require('daisyui'),
  ],
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
}