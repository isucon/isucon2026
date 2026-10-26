import { defineConfig, devices } from "@playwright/test";

export const bootstrapChromeVersion = "149.0.7827.55";
export const bootstrapImage =
  "mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48";

const expectedVersion = process.env.PLAYWRIGHT_CHROME_VERSION ??
  bootstrapChromeVersion;
const expectedImage = process.env.PLAYWRIGHT_IMAGE ?? bootstrapImage;
const testPort = process.env.PLAYWRIGHT_PORT ?? "5173";
if (!/^\d+$/.test(testPort)) throw new Error("PLAYWRIGHT_PORT must be numeric");

const composeE2E = process.env.ISUREAL_COMPOSE_E2E === "1";
if (!/^\d+\.\d+\.\d+\.\d+$/.test(expectedVersion)) {
  throw new Error(
    "PLAYWRIGHT_CHROME_VERSION must be an exact four-part Chrome version",
  );
}
if (!expectedImage.includes("@sha256:")) {
  throw new Error("PLAYWRIGHT_IMAGE must use an immutable sha256 digest");
}

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: "list",
  globalSetup: "./scripts/playwright-global-setup.ts",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${testPort}`,
    trace: "on-first-retry",
    permissions: composeE2E ? ["camera", "geolocation"] : [],
    geolocation: composeE2E
      ? { latitude: 35.681236, longitude: 139.767125 }
      : undefined,
    launchOptions: composeE2E
      ? {
        args: [
          "--use-fake-device-for-media-stream",
          "--use-fake-ui-for-media-stream",
        ],
      }
      : undefined,
    ...devices["Desktop Chrome"],
  },
  projects: [{
    name: `chrome-${expectedVersion}`,
    use: { browserName: "chromium" },
  }],
  webServer: {
    command: `npm run dev -- --port ${testPort}`,
    url: `http://127.0.0.1:${testPort}`,
    reuseExistingServer: !process.env.CI,
  },
});
