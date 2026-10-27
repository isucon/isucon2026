import type { Context } from "hono";
export interface Session {
  user_id: string;
  region: string;
  token_hash: string;
  created_at: string;
}
export interface AdminSession {
  administrator_id: string;
  token_hash: string;
  created_at: string;
}
export type Env = {
  Variables: {
    session: Session;
    adminSession: AdminSession;
    username: string;
  };
};
export type C = Context<Env>;
