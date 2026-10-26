import { Brand } from '../../components/UI';
import { useEffect, useRef, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { login } from '../../api/account';
import { getSecretKey } from '../../app/auth';
import { SecretKeyPanel } from './SecretKeyPanel';

export function LoginPage(): React.JSX.Element {
  const [username, setUsername] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [storedSecretKey, setStoredSecretKey] = useState('');
  const [error, setError] = useState('');
  const navigate = useNavigate();
  const location = useLocation();
  const usernameInput = useRef<HTMLInputElement>(null);
  const focusUsername = location.state?.focusUsername === true;
  useEffect(() => {
    if (focusUsername) usernameInput.current?.focus();
  }, [location.key, focusUsername]);
  useEffect(() => {
    void getSecretKey().then((value) => {
      if (!value) return;
      setStoredSecretKey(value);
      setSecretKey((current) => current || value);
    });
  }, []);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    try {
      await login(username, secretKey);
      navigate('/');
    } catch {
      setError('ユーザー名またはSecretKeyが正しくありません。');
    }
  }
  return (
    <main className='auth-page'>
      <Brand />
      <form onSubmit={(event) => void submit(event)}>
        <h1>ログイン</h1>
        <label>
          ユーザー名<input
            ref={usernameInput}
            data-login-focus
            required
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </label>
        <label>
          SecretKey<input
            required
            value={secretKey}
            onChange={(event) => setSecretKey(event.target.value)}
          />
        </label>
        {error && <p role='alert'>{error}</p>}
        <button>ログイン</button>
        <Link to='/register'>新規登録</Link>
      </form>
      {storedSecretKey && <SecretKeyPanel secretKey={storedSecretKey} />}
    </main>
  );
}
