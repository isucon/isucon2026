import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PostComposer } from '../../src/features/posts/PostComposer';
import { Brand } from '../../src/components/UI';
import { RealMojiPanel } from '../../src/features/interactions/RealMojiPanel';
import { FeedPage } from '../../src/features/feeds/FeedPage';
import { UserSearchPage } from '../../src/features/users/UserSearchPage';
import * as interactions from '../../src/api/interactions';
import * as posts from '../../src/api/posts';
import { APIError } from '../../src/api/client';
vi.mock(
  '../../src/api/interactions',
  () => ({
    listRealMojis: vi.fn(async () => ({ items: [], nextPage: null })),
    replaceRealMoji: vi.fn(),
    deleteRealMoji: vi.fn(),
  }),
);
vi.mock(
  '../../src/api/posts',
  () => ({
    createPost: vi.fn(),
    getFeed: vi.fn(async () => ({ items: [], nextPage: null })),
    getCurrentPostingWindow: vi.fn(async () => ({
      window: { id: 'w' },
      hasPosted: true,
      canPost: false,
      deletionCount: 0,
    })),
  }),
);
vi.mock(
  '../../src/api/relationships',
  () => ({ searchUsers: vi.fn(async () => ({ items: [], nextPage: null })) }),
);
let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  vi.stubGlobal('URL', {
    createObjectURL: vi.fn(() => 'blob:preview'),
    revokeObjectURL: vi.fn(),
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});
async function render(node: React.ReactNode) {
  await act(async () => root.render(<MemoryRouter>{node}</MemoryRouter>));
}
const button = (text: string) =>
  [...host.querySelectorAll('button')].find((b) => b.textContent === text)!;
it('RealMoji file selection previews without uploading, retry retains the chosen image', async () => {
  await render(<RealMojiPanel postId='p' />);
  const input = host.querySelector('input[type=file]')!;
  const file = new File(['image'], 'selfie.jpg', { type: 'image/jpeg' });
  Object.defineProperty(input, 'files', { value: [file] });
  await act(async () =>
    input.dispatchEvent(new Event('change', { bubbles: true }))
  );
  expect(interactions.replaceRealMoji).not.toHaveBeenCalled();
  expect(host.querySelector('img[alt="RealMojiのプレビュー"]')).not.toBeNull();
  vi.mocked(interactions.replaceRealMoji).mockRejectedValueOnce(
    new Error('network'),
  );
  await act(async () => button('送信').click());
  expect(host.textContent).toContain('送信できません');
  expect(host.querySelector('img[alt="RealMojiのプレビュー"]')).not.toBeNull();
  vi.mocked(interactions.replaceRealMoji).mockResolvedValueOnce(
    {} as interactions.RealMoji,
  );
  await act(async () => button('送信').click());
  expect(interactions.replaceRealMoji).toHaveBeenLastCalledWith(
    'p',
    'like',
    file,
  );
  expect(host.querySelector('img[alt="RealMojiのプレビュー"]')).toBeNull();
});
it('unposted feed is locked and never presented as an empty friend feed', async () => {
  vi.mocked(posts.getCurrentPostingWindow).mockResolvedValueOnce(
    {
      window: { id: 'w' },
      hasPosted: false,
      canPost: true,
      deletionCount: 0,
    } as posts.CurrentPostingWindow,
  );
  await render(<FeedPage feed='friends' />);
  expect(host.textContent).toContain('まずは、あなたの一枚。');
  expect(host.querySelector('a[href="/posts/new"]')).not.toBeNull();
  expect(posts.getFeed).not.toHaveBeenCalled();
});
it('network failures have retry instead of an unposted message', async () => {
  vi.mocked(posts.getFeed).mockRejectedValueOnce(
    new APIError(500, { error: 'internal_error' }),
  );
  await render(<FeedPage feed='friends' />);
  expect(host.textContent).not.toContain('まずは、あなたの一枚。');
  await act(async () => button('再読み込み').click());
  expect(host.textContent).toContain('まだ、一枚もありません。');
});
it('search distinguishes initial state and zero results', async () => {
  await render(<UserSearchPage />);
  expect(host.textContent).not.toContain('見つかりませんでした');
  await act(async () =>
    host.querySelector('form')!.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    )
  );
  expect(host.textContent).toContain('見つかりませんでした');
});

it("feed renders the returned post image without using the author's profile image as an inset", async () => {
  const post = {
    id: 'p',
    author: {
      id: 'u',
      username: 'alice',
      displayName: 'Alice',
      accountType: 'regular',
      profileImageUrl: '/profile.jpg',
    },
    imageUrl: '/photo.jpg',
    postingWindowId: 'w',
    caption: '',
    visibility: 'friends' as const,
    location: { kind: 'none' as const },
    postedAt: '2026-10-26T00:00:00Z',
    onTime: true,
    lateSeconds: 0,
  };
  vi.mocked(posts.getFeed).mockResolvedValueOnce({
    items: [post],
    nextPage: null,
  });
  await render(<FeedPage feed='friends' />);
  expect(host.querySelectorAll('img')).toHaveLength(1);
  expect(host.querySelector('img')?.getAttribute('src')).toBe('/photo.jpg');
});

it('brand uses the extracted vector logo and links home', async () => {
  await render(<Brand />);
  const logo = host.querySelector('img[alt="ISUReal"]');
  expect(logo?.getAttribute('src')).toBe('/isureal-logo.svg');
  expect(logo?.closest('a')?.getAttribute('href')).toBe('/');
});

it.each(
  [
    [
      {
        window: { id: 'w' },
        hasPosted: true,
        canPost: false,
        deletionCount: 0,
      },
      '今日はすでに投稿済みです。',
      true,
    ],
    [
      {
        window: { id: 'w' },
        hasPosted: false,
        canPost: false,
        deletionCount: 2,
      },
      '削除回数の上限に達したため、今日は再投稿できません。',
      false,
    ],
    [
      { window: null, hasPosted: false, canPost: false, deletionCount: 0 },
      '現在は投稿できる期間ではありません。',
      false,
    ],
  ] as const,
)(
  'post slot conflict explains the current restriction and preserves the photo',
  async (state, message, hasPost) => {
    vi.mocked(posts.createPost).mockRejectedValueOnce(
      new APIError(409, { error: 'post_slot_unavailable' }),
    );
    vi.mocked(posts.getCurrentPostingWindow).mockResolvedValueOnce(
      state as posts.CurrentPostingWindow,
    );
    await render(<PostComposer />);
    const input = host.querySelector('input[type=file]')!;
    Object.defineProperty(input, 'files', {
      value: [new File(['image'], 'photo.jpg', { type: 'image/jpeg' })],
    });
    await act(async () =>
      input.dispatchEvent(new Event('change', { bubbles: true }))
    );
    await act(async () =>
      host.querySelector('form')!.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true }),
      )
    );
    expect(
      [...host.querySelectorAll('[role="alert"]')].map((el) => el.textContent)
        .join(' '),
    ).toContain(message);
    expect(Boolean(host.querySelector('a[href="/posts/me/current"]'))).toBe(
      hasPost,
    );
    expect(host.querySelector('img[alt="投稿画像のプレビュー"]')).not
      .toBeNull();
  },
);

function fakeCamera() {
  const stop = vi.fn();
  const getUserMedia = vi.fn(async () => ({ getTracks: () => [{ stop }] }));
  vi.stubGlobal('navigator', { mediaDevices: { getUserMedia } });
  const drawImage = vi.fn();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
    { drawImage } as unknown as CanvasRenderingContext2D,
  );
  const sizes: number[][] = [];
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(
    function (this: HTMLCanvasElement, callback) {
      sizes.push([this.width, this.height]);
      callback(new Blob(['jpeg'], { type: 'image/jpeg' }));
    },
  );
  return { stop, getUserMedia, sizes };
}
async function firstFrame() {
  const video = host.querySelector('video')!;
  Object.defineProperties(video, {
    videoWidth: { value: 640, configurable: true },
    videoHeight: { value: 480, configurable: true },
    readyState: { value: 2, configurable: true },
  });
  await act(async () => video.dispatchEvent(new Event('loadeddata')));
}
it('RealMoji waits for a frame and encodes camera images within the API size limit', async () => {
  const camera = fakeCamera();
  await render(<RealMojiPanel postId='p' />);
  await act(async () => button('カメラを開始').click());
  expect(button('撮影する').disabled).toBe(true);
  await firstFrame();
  await act(async () => button('撮影する').click());
  expect(camera.sizes).toEqual([[512, 384]]);
  expect(camera.stop).toHaveBeenCalled();
});
it('retaking a post restarts the camera and leaving its step stops the stream', async () => {
  const camera = fakeCamera();
  await render(<PostComposer />);
  expect(button('撮影する').disabled).toBe(true);
  await firstFrame();
  await act(async () => button('撮影する').click());
  expect(camera.stop).toHaveBeenCalledTimes(1);
  await act(async () => button('撮り直す').click());
  expect(camera.getUserMedia).toHaveBeenCalledTimes(2);
  expect(button('撮影する').disabled).toBe(true);
  await act(async () => button('この写真で続ける').click());
  expect(camera.stop).toHaveBeenCalledTimes(2);
});
it('a settings deep link with no image redirects to the visible camera', async () => {
  const camera = fakeCamera();
  await act(async () =>
    root.render(
      <MemoryRouter initialEntries={['/posts/new?step=settings']}>
        <PostComposer />
      </MemoryRouter>,
    )
  );
  expect(host.querySelector('h1')?.textContent).toBe('撮影');
  expect(host.querySelector('video')?.closest('[hidden]')).toBeNull();
  expect(camera.getUserMedia).toHaveBeenCalledTimes(1);
});
it('own-post RealMoji keeps moderation but hides creation', async () => {
  vi.mocked(interactions.listRealMojis).mockResolvedValueOnce({
    items: [
      {
        id: 'r',
        imageUrl: '/r.jpg',
        reactor: { displayName: 'Bob' },
        reactionType: 'like',
      } as interactions.RealMoji,
    ],
    nextPage: null,
  });
  await render(<RealMojiPanel postId='p' canCreate={false} />);
  expect(host.querySelector('input[type=file]')).toBeNull();
  expect(host.textContent).not.toContain('RealMojiを作成');
  expect(button('削除')).toBeDefined();
});
it('feed pagination does not refetch posting-window state', async () => {
  vi.mocked(posts.getFeed).mockResolvedValueOnce({
    items: [],
    nextPage: 'cursor',
  });
  await render(<FeedPage feed='friends' />);
  await act(async () => button('さらに読み込む').click());
  expect(posts.getCurrentPostingWindow).toHaveBeenCalledTimes(1);
  expect(posts.getFeed).toHaveBeenLastCalledWith('friends', 'cursor');
});
