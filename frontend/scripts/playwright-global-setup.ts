import { chromium } from '@playwright/test';
import { bootstrapChromeVersion } from '../playwright.config';

export default async function globalSetup(): Promise<void> {
  const expected = process.env.PLAYWRIGHT_CHROME_VERSION ?? bootstrapChromeVersion;
  const browser = await chromium.launch();
  const actual = browser.version();
  await browser.close();
  if (actual !== expected) {
    throw new Error(`Chrome version mismatch: expected ${expected}, got ${actual}`);
  }
}
