import { describe, expect, it, vi } from 'vitest';
import { randomUUID } from './ids';

describe('randomUUID', () => {
  it('uses the native implementation when available', () => {
    const native = vi.fn(() => '123e4567-e89b-42d3-a456-426614174000' as `${string}-${string}-${string}-${string}-${string}`);
    expect(randomUUID({ randomUUID: native, getRandomValues: vi.fn() })).toBe(
      '123e4567-e89b-42d3-a456-426614174000',
    );
    expect(native).toHaveBeenCalledOnce();
  });

  it('generates a v4 UUID when randomUUID is unavailable', () => {
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.set([0, 1, 2, 3, 4, 5, 0xff, 7, 0xff, 9, 10, 11, 12, 13, 14, 15]);
      return bytes;
    });

    expect(randomUUID({ getRandomValues })).toBe('00010203-0405-4f07-bf09-0a0b0c0d0e0f');
    expect(getRandomValues).toHaveBeenCalledOnce();
  });
});
