import { defineConfig } from 'tsup'

// Dual ESM+CJS with a declaration file per format. The packaging itself is
// gated by publint and arethetypeswrong in CI, because a package that only
// works under one resolver is the bug this setup exists to prevent.
export default defineConfig({
  entry: ['src/index.ts'],
  format: ['esm', 'cjs'],
  dts: true,
  sourcemap: false,
  clean: true,
  target: 'node18',
  platform: 'node',
})
