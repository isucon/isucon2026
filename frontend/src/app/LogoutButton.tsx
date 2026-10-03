import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { apiFetch } from '../api/client';
import { clearToken } from './auth';

export function LogoutButton(): React.JSX.Element {
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  async function logout(): Promise<void> {
    setBusy(true);
    try {
      await apiFetch('/auth/logout', { method: 'POST' });
    } finally {
      await clearToken();
      navigate('/login', { replace: true, state: { focusUsername: true } });
      setBusy(false);
    }
  }
  return (
    <button type="button" className="logout-button" disabled={busy} onClick={() => void logout()}>
      {busy ? 'ログアウト中…' : 'ログアウト'}
    </button>
  );
}
