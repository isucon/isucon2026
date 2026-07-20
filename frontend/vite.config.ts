/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { resolve } from 'node:path';

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, strictPort: true, headers: {
    'Content-Security-Policy': "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' blob:; connect-src 'self' ws:; worker-src 'self' blob:; media-src 'self' blob:",
    'Permissions-Policy': 'camera=(self)',
    'Referrer-Policy': 'no-referrer',
    'X-Content-Type-Options': 'nosniff',
  }, proxy: { '/api': 'http://127.0.0.1:8080' } },
  build: {
    rollupOptions: {
      input: { app: resolve(import.meta.dirname, 'index.html'), 'service-worker': resolve(import.meta.dirname, 'src/service-worker/push.ts') },
      output: { entryFileNames: (chunk) => chunk.name === 'service-worker' ? 'service-worker.js' : 'assets/[name]-[hash].js' },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}', 'tests/unit/**/*.test.{ts,tsx}'],
  },
});
