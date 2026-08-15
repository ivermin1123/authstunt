import { defineConfig } from 'tsup'

// Same shape as @authstunt/client: dual ESM+CJS with a declaration file per
// format, gated by publint and arethetypeswrong. @playwright/test is a peer
// dependency and must stay external, or a suite would end up with a second
// copy of the runner and fixtures registered against the wrong one.
export default defineConfig({
  entry: ['src/index.ts'],
  format: ['esm', 'cjs'],
  dts: true,
  sourcemap: false,
  clean: true,
  target: 'node18',
  platform: 'node',
  external: ['@playwright/test'],
})
