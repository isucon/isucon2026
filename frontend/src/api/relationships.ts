import { pageRequest } from './pagination';
import { jsonRequest } from './client';
export interface UserSummary{id:string;username:string;displayName:string;profileImageUrl:string|null;bio?:string;accountType:'regular'|'certified_public'}
export interface RelationshipItem{relationId:string;user:UserSummary;status:string;updatedAt:string}
export type { Page } from './pagination';

export const searchUsers = (q: string, next?: string) => pageRequest<UserSummary>(`/users?q=${encodeURIComponent(q)}`, next, user => ({ beforeUsername: user.username, beforeId: user.id }));
export const getUser=(id:string)=>jsonRequest<UserSummary>(`/users/${id}`);
export const createFriendRequest=(recipientId:string)=>jsonRequest<{relationId:string;status:string}>('/friend-requests',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({recipientId})});
export const transitionFriendRequest=(id:string,action:'accept'|'reject'|'cancel')=>jsonRequest(`/friend-requests/${id}/${action}`,{method:'POST',});
export const listRelationships = (kind: string, next?: string) => pageRequest<RelationshipItem>(`/relationships/${kind}`, next, item => ({ beforeUpdatedAt: item.updatedAt, beforeId: item.relationId }));
export const relationshipMutation=(resource:'friendships'|'follows'|'blocks',id:string,method:'PUT'|'DELETE')=>jsonRequest<void>(`/${resource}/${id}`,{method,});
