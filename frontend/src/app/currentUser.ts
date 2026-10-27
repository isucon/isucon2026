import { createContext, useContext } from 'react';
import type { Me } from '../api/account';
export const CurrentUserContext = createContext<Me | null>(null);
export const useCurrentUser = () => useContext(CurrentUserContext);
