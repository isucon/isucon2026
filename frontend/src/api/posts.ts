import { jsonRequest } from './client';
import { randomUUID } from './ids';

export type Visibility = 'friends' | 'friends_of_friends' | 'public';
export type FeedKind = 'friends' | 'friends-of-friends' | 'public' | 'following';
export type Location =
  | { kind: 'none' }
  | { kind: 'exact'; latitude: number; longitude: number }
  | { kind: 'approximate'; locality: string };

export interface Post {
  id: string;
  author: { id: string; username: string; displayName: string; accountType: string };
  postingWindowId: string;
  imageUrl: string;
  caption: string;
  visibility: Visibility;
  location: Location;
  postedAt: string;
  onTime: boolean;
  lateSeconds: number;
}

export interface PostPage { items: Post[]; nextCursor: string | null }

const mutationHeaders = (): HeadersInit => ({ 'Idempotency-Key': randomUUID() });

export function createPost(input: { image: File; caption: string; visibility: Visibility; location: Location }): Promise<Post> {
  const body = new FormData();
  body.set('image', input.image);
  body.set('caption', input.caption);
  body.set('visibility', input.visibility);
  body.set('location', new Blob([JSON.stringify(input.location)], { type: 'application/json' }), 'location.json');
  return jsonRequest<Post>('/posts', { method: 'POST', headers: mutationHeaders(), body });
}

export function getMyCurrentPost(): Promise<Post> {
  return jsonRequest<Post>('/posts/me/current');
}
export function getPost(postId:string):Promise<Post>{return jsonRequest<Post>(`/posts/${postId}`)}

export function updatePost(postId: string, input: { caption?: string; visibility?: Visibility }): Promise<Post> {
  return jsonRequest<Post>(`/posts/${postId}`, {
    method: 'PATCH', headers: { ...mutationHeaders(), 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  });
}

export function deletePost(postId: string): Promise<void> {
  return jsonRequest<void>(`/posts/${postId}`, { method: 'DELETE', headers: mutationHeaders() });
}

export function getFeed(feed: FeedKind, cursor?: string): Promise<PostPage> {
  const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : '';
  return jsonRequest<PostPage>(`/feeds/${feed}${query}`);
}
