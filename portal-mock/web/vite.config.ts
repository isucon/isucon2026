import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

const apiTarget = process.env.PORTAL_API_TARGET ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [react()],
  resolve: {
    preserveSymlinks: true,
  },
  server: {
    host: '0.0.0.0',
    port: 5174,
    strictPort: true,
    proxy: {
      '/portal_mock.v1.PortalMockService': {
        target: apiTarget,
        changeOrigin: false,
      },
      '/portal.v1.PortalRouterService': {
        target: apiTarget,
        changeOrigin: false,
      },
      '/healthz': {
        target: apiTarget,
        changeOrigin: false,
      },
    },
  },
  preview: {
    host: '0.0.0.0',
    port: 4174,
    strictPort: true,
  },
});
