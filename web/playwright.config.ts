import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 90_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:18090",
    locale: "zh-CN",
    timezoneId: "Asia/Shanghai",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command:
      "bash -c 'rm -rf /tmp/kpcc-e2e && mkdir -p /tmp/kpcc-e2e/deploy && KPCC_DATA_DIR=/tmp/kpcc-e2e KPCC_LISTEN=127.0.0.1:18090 KPCC_DEV=1 KPCC_FAKE_ACME=1 KPCC_SETUP_TOKEN=e2e-token KPCC_DEPLOY_ROOTS=/tmp/kpcc-e2e/deploy go run ./cmd/kpccssl serve'",
    cwd: "..",
    url: "http://127.0.0.1:18090/api/v1/system/health",
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
