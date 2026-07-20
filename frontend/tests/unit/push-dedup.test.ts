import { describe, expect, it } from 'vitest';

describe('Push の重複表示抑止', () => {
  it('同じ論理通知 ID を安定した tag にする', async () => {
    const listeners = new Map<string, EventListener>();
    Object.assign(globalThis, { self: { addEventListener: (name: string, listener: EventListener) => listeners.set(name, listener) } });
    const { notificationOptions } = await import('../../src/service-worker/push');
    expect(notificationOptions({ notificationId: 'logical-id' }).tag).toBe('logical-id');
    expect(notificationOptions({ notificationId: 'logical-id' }).data).toEqual({ notificationId: 'logical-id' });
  });
});
