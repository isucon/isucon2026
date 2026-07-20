import { jsonRequest } from './client';
export interface UserSummary{id:string;username:string;displayName:string;profileImageUrl:string|null;bio?:string;accountType:'regular'|'certified_public'}
export interface RelationshipItem{relationId:string;user:UserSummary;status:string;updatedAt:string}
export interface Page<T>{items:T[];nextCursor:string|null}
const headers=():HeadersInit=>({'Idempotency-Key':crypto.randomUUID()});
export const searchUsers=(q:string,cursor?:string)=>jsonRequest<Page<UserSummary>>(`/users?q=${encodeURIComponent(q)}${cursor?`&cursor=${encodeURIComponent(cursor)}`:''}`);
export const getUser=(id:string)=>jsonRequest<UserSummary>(`/users/${id}`);
export const createFriendRequest=(recipientId:string)=>jsonRequest<{relationId:string;status:string}>('/friend-requests',{method:'POST',headers:{...headers(),'Content-Type':'application/json'},body:JSON.stringify({recipientId})});
export const transitionFriendRequest=(id:string,action:'accept'|'reject'|'cancel')=>jsonRequest(`/friend-requests/${id}/${action}`,{method:'POST',headers:headers()});
export const listRelationships=(kind:string,cursor?:string)=>jsonRequest<Page<RelationshipItem>>(`/relationships/${kind}${cursor?`?cursor=${encodeURIComponent(cursor)}`:''}`);
export const relationshipMutation=(resource:'friendships'|'follows'|'blocks',id:string,method:'PUT'|'DELETE')=>jsonRequest<void>(`/${resource}/${id}`,{method,headers:headers()});
