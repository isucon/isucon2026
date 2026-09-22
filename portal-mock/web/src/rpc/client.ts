import { createClient, type Client } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { PortalMockService } from '@isucon/portal-proto/portal_mock/v1/portal_mock_pb';

export type PortalMockClient = Client<typeof PortalMockService>;

const baseUrl =
  (import.meta.env.VITE_PORTAL_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ??
  window.location.origin;

export const portalTransport = createConnectTransport({ baseUrl });
export const portalClient: PortalMockClient = createClient(PortalMockService, portalTransport);
