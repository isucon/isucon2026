import { expect, type Page } from '@playwright/test';

const mockService = '/portal_mock.v1.PortalMockService';

export async function requirePortalServer(page: Page) {
  const response = await page.request.get('/healthz').catch(() => null);
  if (!response?.ok()) {
    throw new Error(
      'Portal mock server が起動していません。先に server を :8080 で起動してください。',
    );
  }
}

export async function resetPortal(page: Page) {
  const response = await page.request.post(`${mockService}/Reset`, {
    headers: {
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    },
    data: {},
  });
  expect(response.ok()).toBeTruthy();
}

export async function getPortalState(page: Page) {
  const response = await page.request.post(`${mockService}/GetState`, {
    headers: {
      'Content-Type': 'application/json',
      'Connect-Protocol-Version': '1',
    },
    data: {},
  });
  expect(response.ok()).toBeTruthy();
  return response.json() as Promise<Record<string, unknown>>;
}
