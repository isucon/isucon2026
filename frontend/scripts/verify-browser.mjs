import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

const require = createRequire(import.meta.url);
const packagePath = require.resolve('playwright-core/package.json');
const browsers = JSON.parse(readFileSync(join(dirname(packagePath), 'browsers.json'), 'utf8'));

const expectedVersion = process.env.PLAYWRIGHT_CHROME_VERSION ?? '149.0.7827.55';
const expectedImage = process.env.PLAYWRIGHT_IMAGE ??
  'mcr.microsoft.com/playwright:v1.61.1-noble@sha256:5b8f294aff9041b7191c34a4bab3ac270157a28774d4b0660e9743297b697e48';
const chromium = browsers.browsers.find((browser) => browser.name === 'chromium');
if (chromium?.browserVersion !== expectedVersion) {
  throw new Error(`Playwright Chromium mismatch: expected ${expectedVersion}, package has ${chromium?.browserVersion}`);
}
if (!expectedImage.includes('@sha256:')) {
  throw new Error('PLAYWRIGHT_IMAGE must be pinned by sha256 digest');
}
console.log(`Chrome ${expectedVersion}; image ${expectedImage}`);
