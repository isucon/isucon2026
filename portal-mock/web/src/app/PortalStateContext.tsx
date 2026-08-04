import { createContext, useContext, type PropsWithChildren } from 'react';
import {
  usePortalState,
  type PortalStateController,
  type UsePortalStateOptions,
} from './usePortalState';

export const PortalStateContext = createContext<PortalStateController | null>(null);

export interface PortalStateProviderProps extends PropsWithChildren {
  controller?: PortalStateController;
  options?: UsePortalStateOptions;
}

function LivePortalStateProvider({
  children,
  options,
}: Pick<PortalStateProviderProps, 'children' | 'options'>) {
  const liveController = usePortalState(options);
  return (
    <PortalStateContext.Provider value={liveController}>
      {children}
    </PortalStateContext.Provider>
  );
}

export function PortalStateProvider({
  children,
  controller,
  options,
}: PortalStateProviderProps) {
  if (!controller) {
    return <LivePortalStateProvider options={options}>{children}</LivePortalStateProvider>;
  }
  return (
    <PortalStateContext.Provider value={controller}>
      {children}
    </PortalStateContext.Provider>
  );
}

export function usePortalStateContext(): PortalStateController {
  const context = useContext(PortalStateContext);
  if (!context) {
    throw new Error('usePortalStateContext must be used within PortalStateProvider');
  }
  return context;
}
