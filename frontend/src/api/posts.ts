import { pageRequest } from './pagination';
import { jsonRequest } from './client';

export type Visibility = 'friends' | 'friends_of_friends' | 'public';
export type FeedKind =
  | 'friends'
  | 'friends-of-friends'
  | 'public'
  | 'following';
export type Location =
  | { kind: 'none' }
  | { kind: 'exact'; latitude: number; longitude: number }
  | { kind: 'approximate'; locality: string };

export interface Post {
  id: string;
  author: {
    id: string;
    username: string;
    displayName: string;
    accountType: string;
  };
  postingWindowId: string;
  imageUrl: string;
  caption: string;
  visibility: Visibility;
  location: Location;
  postedAt: string;
  onTime: boolean;
  lateSeconds: number;
  score?: number;
}

export interface PostPage {
  items: Post[];
  nextPage: string | null;
}

export function createPost(
  input: {
    image: File;
    caption: string;
    visibility: Visibility;
    location: Location;
  },
): Promise<Post> {
  const body = new FormData();
  body.set('image', input.image);
  body.set('caption', input.caption);
  body.set('visibility', input.visibility);
  body.set('location_kind', input.location.kind);
  if (input.location.kind === 'exact') {
    body.set('latitude', String(input.location.latitude));
    body.set('longitude', String(input.location.longitude));
  }
  if (input.location.kind === 'approximate') {
    body.set('locality', input.location.locality);
  }
  return jsonRequest<Post>('/posts', { method: 'POST', body });
}

export function getMyCurrentPost(): Promise<Post> {
  return jsonRequest<Post>('/posts/me/current');
}
export function getPost(postId: string): Promise<Post> {
  return jsonRequest<Post>(`/posts/${postId}`);
}

export function updatePost(
  postId: string,
  input: { caption?: string; visibility?: Visibility },
): Promise<Post> {
  return jsonRequest<Post>(`/posts/${postId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
}

export function deletePost(postId: string): Promise<void> {
  return jsonRequest<void>(`/posts/${postId}`, { method: 'DELETE' });
}

export function getFeed(feed: FeedKind, next?: string): Promise<PostPage> {
  return pageRequest<Post>(
    `/feeds/${feed}`,
    next,
    (post) => ({
      ...(post.score === undefined ? {} : { beforeScore: String(post.score) }),
      beforePostedAt: post.postedAt,
      beforeId: post.id,
    }),
  );
}

export interface CurrentPostingWindow {
  window: {
    id: string;
    region: string;
    localDate: string;
    startsAt: string;
    onTimeDeadline: string;
  } | null;
  hasPosted: boolean;
  canPost: boolean;
  deletionCount: number;
}
export const getCurrentPostingWindow = () =>
  jsonRequest<CurrentPostingWindow>('/posting-window/current');
