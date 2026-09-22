import { useCallback, useEffect, useState } from 'react';

const databaseName = 'isureal-session';
const storeName = 'credentials';
const tokenKey = 'user-token';
const secretKeyName = 'user-secret-key';
const sessionEvent = 'isureal:session-changed';

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1);
    request.onupgradeneeded = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(storeName)) database.createObjectStore(storeName);
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('Unable to open the session database'));
  });
}

async function transact<T>(mode: IDBTransactionMode, operation: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, mode);
    const request = operation(transaction.objectStore(storeName));
    let result: T;
    request.onsuccess = () => { result = request.result; };
    request.onerror = () => reject(request.error ?? new Error('Session storage operation failed'));
    transaction.oncomplete = () => { database.close(); resolve(result); };
    transaction.onerror = () => reject(transaction.error ?? new Error('Session transaction failed'));
    transaction.onabort = () => reject(transaction.error ?? new Error('Session transaction aborted'));
  });
}

export async function getToken(): Promise<string | null> {
  const value = await transact<IDBValidKey | undefined>('readonly', (store) => store.get(tokenKey));
  return typeof value === 'string' ? value : null;
}

export async function getSecretKey(): Promise<string | null> {
  const value = await transact<IDBValidKey | undefined>('readonly', (store) => store.get(secretKeyName));
  return typeof value === 'string' && value !== '' ? value : null;
}

export async function replaceSecretKey(secretKey: string): Promise<void> {
  if (!secretKey) throw new Error('An empty secret key cannot be persisted');
  await transact<IDBValidKey>('readwrite', (store) => store.put(secretKey, secretKeyName));
}

export async function replaceToken(token: string): Promise<void> {
  if (!token) throw new Error('An empty session token cannot be persisted');
  await transact<IDBValidKey>('readwrite', (store) => store.put(token, tokenKey));
  window.dispatchEvent(new Event(sessionEvent));
}

export async function clearToken(): Promise<void> {
  await transact<undefined>('readwrite', (store) => store.delete(tokenKey));
  window.dispatchEvent(new Event(sessionEvent));
}

export function useSession(): { loading: boolean; authenticated: boolean; refresh: () => Promise<void> } {
  const [loading, setLoading] = useState(true);
  const [authenticated, setAuthenticated] = useState(false);
  const refresh = useCallback(async () => {
    try { setAuthenticated((await getToken()) !== null); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => {
    void refresh();
    const listener = () => { void refresh(); };
    window.addEventListener(sessionEvent, listener);
    window.addEventListener('isureal:session-invalidated', listener);
    return () => {
      window.removeEventListener(sessionEvent, listener);
      window.removeEventListener('isureal:session-invalidated', listener);
    };
  }, [refresh]);
  return { loading, authenticated, refresh };
}
