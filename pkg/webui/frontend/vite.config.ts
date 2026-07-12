import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

const embeddedGhosttyWasmPattern = /new URL\("data:application\/wasm;base64,[^"]+", self\.location\)/

const externalizeGhosttyWasm = (): Plugin => ({
  name: 'externalize-ghostty-wasm',
  enforce: 'pre',
  transform(code, id) {
    if (!id.includes('/node_modules/ghostty-web/dist/ghostty-web.js')) {
      return null
    }

    if (!embeddedGhosttyWasmPattern.test(code)) {
      this.error('ghostty-web no longer contains the expected embedded WASM fallback')
    }

    return {
      code: code.replace(
        embeddedGhosttyWasmPattern,
        'new URL("./ghostty-vt.wasm", import.meta.url)'
      ),
      map: null
    }
  }
})

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [externalizeGhosttyWasm(), react()],

  // Build configuration
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    sourcemap: false,
    minify: 'esbuild',

    // Keep long-lived dependencies and optional features in stable chunks.
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: 'react-vendor',
              test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/,
              priority: 40
            },
            {
              name: 'router-vendor',
              test: /node_modules[\\/](react-router|react-router-dom)[\\/]/,
              priority: 30
            },
            {
              name: 'icons-vendor',
              test: /node_modules[\\/]lucide-react[\\/]/,
              priority: 20
            },
            {
              name: 'markdown-vendor',
              test: /node_modules[\\/]marked[\\/]/,
              priority: 20
            },
            {
              name: 'utils-vendor',
              test: /node_modules[\\/](date-fns|clsx)[\\/]/,
              priority: 20
            }
          ]
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
