import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const baseURL = process.env.READYSPEC_E2E_BASE_URL ?? 'http://127.0.0.1:18581'
const workspace = mkdtempSync(join(tmpdir(), 'readyspec-web-e2e-'))

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  use: {
    baseURL,
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
  },
  ...(process.env.READYSPEC_E2E_EXTERNAL_SERVER === '1'
    ? {}
    : {
        webServer: {
          command: `go run ./e2e/seed_cleanup.go && go run ../cmd/readyspec serve --no-browser --listen ${new URL(baseURL).host}`,
          url: baseURL,
          timeout: 60_000,
          reuseExistingServer: false,
          gracefulShutdown: { signal: 'SIGTERM', timeout: 10_000 },
          env: {
            READYSPEC_WORKSPACE: workspace,
            READYSPEC_AUTH_TOKEN: 'readyspec-browser-test-token-only-000000000000000000',
          },
        },
      }),
})
