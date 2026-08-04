import { PortalPage } from './PortalPage';
import { PortalStateProvider, type PortalStateProviderProps } from './PortalStateContext';

export interface AppProps {
  controller?: PortalStateProviderProps['controller'];
}

export function App({ controller }: AppProps) {
  return (
    <PortalStateProvider controller={controller}>
      <PortalPage />
    </PortalStateProvider>
  );
}
