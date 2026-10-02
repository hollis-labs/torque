import { defineConfig } from 'vitest/config'
import path from 'node:path'

export default defineConfig({
  test: {
    globals: false,
    // Keep DOM renders within their assertion deadlines on shared dev/CI hosts.
    // Vitest's CPU-count default starts 11 workers on a 12-core machine.
    minWorkers: 1,
    maxWorkers: 2,
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    environment: 'node',
    setupFiles: ['./src/test-setup.ts'],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})
