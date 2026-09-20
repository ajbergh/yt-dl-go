import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, '.'),
      },
    },
    server: {
      // Optionally disable Vite's hot-module-reload WebSocket.
      // This mock's file watcher may also be disabled when HMR is off.
      hmr: process.env.DISABLE_HMR !== 'true',
      // Also stop watching files when HMR is disabled.
      watch: process.env.DISABLE_HMR === 'true' ? null : {},
    },
  };
});
