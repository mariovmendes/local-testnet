import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    allowedHosts: ['.serveousercontent.com'],
    proxy: {
      '/rpc/chain-a-builder': {
        target: 'http://127.0.0.1:17545',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/rpc\/chain-a-builder/, '') || '/',
      },
      '/rpc/chain-a-reth': {
        target: 'http://127.0.0.1:18545',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/rpc\/chain-a-reth/, '') || '/',
      },
      '/rpc/chain-b-builder': {
        target: 'http://127.0.0.1:27545',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/rpc\/chain-b-builder/, '') || '/',
      },
      '/rpc/chain-b-reth': {
        target: 'http://127.0.0.1:28545',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/rpc\/chain-b-reth/, '') || '/',
      },
      '/sidecar/a': {
        target: 'http://127.0.0.1:17090',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/sidecar\/a/, '') || '/',
      },
      '/sidecar/b': {
        target: 'http://127.0.0.1:27090',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/sidecar\/b/, '') || '/',
      },
      '/health': {
  	target: 'http://127.0.0.1:8090',
  	changeOrigin: true,
  	rewrite: (path) => path.replace(/^\/health/, '') || '/',
  	bypass: () => {
    		return new Response(
      			JSON.stringify({ services: [] }),
      				{ headers: { 'Content-Type': 'application/json' } }
    			)
  		},
	},
      '/bundler/a': {
        target: 'http://127.0.0.1:17082',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/bundler\/a/, '') || '/',
      },
      '/bundler/b': {
        target: 'http://127.0.0.1:27082',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/bundler\/b/, '') || '/',
      },
    },
  },
})

