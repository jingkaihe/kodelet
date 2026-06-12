import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],

  // Build configuration
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    sourcemap: false,
    minify: 'esbuild',

    // Optimize chunks
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules/react/') || id.includes('node_modules/react-dom/')) {
            return 'react-vendor'
          }
          if (id.includes('node_modules/react-router-dom/')) {
            return 'router-vendor'
          }
          if (id.includes('node_modules/marked/') || id.includes('node_modules/prismjs/')) {
            return 'markdown-vendor'
          }
          if (id.includes('node_modules/date-fns/') || id.includes('node_modules/clsx/')) {
            return 'utils-vendor'
          }
        }
      }
    }
  },

  // Development server
  server: {
    port: 3000,
    open: false,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewriteWsOrigin: true,
        secure: false,
        ws: true
      }
    }
  },

  // Path resolution
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src')
    }
  },

  // CSS configuration
  css: {
    postcss: './postcss.config.js'
  }
})
