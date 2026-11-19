import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  // The editor chunk is large but only loads when a file is edited.
  build: { outDir: '../dist', emptyOutDir: true, chunkSizeWarningLimit: 1000 },
  server: { proxy: { '/api': 'http://localhost:8080', '/s': 'http://localhost:8080' } },
})
