import { spawn } from "node:child_process";
import { once } from "node:events";
import { Hono } from "hono";
import { HTTPException } from "hono/http-exception";
import { logger } from "hono/logger";
import type pg from "pg";
import { validRegion } from "./core.js";
import {
  type Env,
  sessionMiddleware,
  adminSessionMiddleware,
  regionMiddleware,
} from "./http.js";
import { userHandlers } from "./user_api.js";
import { postHandlers } from "./post_api.js";
import { relationshipHandlers } from "./relationship_api.js";
import { interactionHandlers } from "./interaction_api.js";
import { imageHandlers } from "./image_api.js";
import { adminHandlers } from "./admin_api.js";
import { memoryHandlers } from "./memory_api.js";
import { notificationsHandler } from "./notification_api.js";
export function createApp(
  db: pg.Pool,
  region: string,
  options: { logging?: boolean } = {},
) {
  if (!validRegion(region)) throw new Error("invalid server region: " + region);
  const app = new Hono<Env>();
  if (options.logging !== false) app.use(logger());
  app.onError((error, c) => {
    const status = error instanceof HTTPException ? error.status : 500;
    if (status >= 500) console.error(error);
    return c.json({ error: error.message }, status);
  });
  app.notFound((c) => c.json({ error: "Not Found" }, 404));
  const auth = sessionMiddleware(db),
    regional = regionMiddleware(region),
    adminAuth = adminSessionMiddleware(db);
  const u = userHandlers(db),
    p = postHandlers(db),
    r = relationshipHandlers(db),
    i = interactionHandlers(db),
    im = imageHandlers(db),
    a = adminHandlers(db),
    m = memoryHandlers(db);
  app.post("/api/initialize", async (c) => {
    const child = spawn("../sql/init.sh", { stdio: "inherit" });
    const [code] = await once(child, "exit");
    if (code !== 0) throw new Error(`init.sh exited with code ${code}`);
    return c.json({ language: "nodejs" });
  });
  app.post("/api/auth/register", u.register);
  app.post("/api/auth/login", u.login);
  app.post("/api/auth/logout", auth, u.logout);
  app.get("/api/users", auth, regional, u.search);
  app.get("/api/users/:userId", auth, regional, u.getUser);
  app.get("/api/me/export", auth, regional, m.export);
  app.get("/api/me", auth, regional, u.getMe);
  app.patch("/api/me", auth, u.updateMe);
  app.put("/api/me/profile-image", auth, im.putProfile);
  app.delete("/api/me/profile-image", auth, im.deleteProfile);
  app.post("/api/me/account-deletion", auth, regional, u.deleteAccount);
  app.post("/api/posts", auth, regional, p.create);
  app.get("/api/posting-window/current", auth, regional, p.window);
  app.get("/api/posts/me/current", auth, regional, p.current);
  app.get("/api/posts/:postId", auth, regional, p.get);
  app.patch("/api/posts/:postId", auth, regional, p.update);
  app.delete("/api/posts/:postId", auth, regional, p.delete);
  app.get("/api/feeds/:feed", auth, regional, p.feed);
  app.get("/api/memories", auth, regional, m.list);
  app.get("/api/memories/:date", auth, regional, m.list);
  app.post("/api/posts/:postId/comments", auth, regional, i.createComment);
  app.get("/api/posts/:postId/comments", auth, regional, i.getComments);
  app.delete("/api/comments/:commentId", auth, regional, i.deleteComment);
  app.post("/api/posts/:postId/realmojis", auth, regional, i.replaceRealMoji);
  app.get("/api/posts/:postId/realmojis", auth, regional, i.getRealMojis);
  app.delete("/api/realmojis/:realMojiId", auth, regional, i.deleteRealMoji);
  app.get("/api/images/:imageId", im.get);
  app.get("/api/notifications", auth, regional, notificationsHandler(db));
  app.post("/api/friend-requests", auth, regional, r.createRequest);
  app.post(
    "/api/friend-requests/:requestId/:action",
    auth,
    regional,
    r.transitionRequest,
  );
  app.get("/api/relationships/:kind", auth, regional, r.list);
  app.delete("/api/friendships/:userId", auth, regional, r.change);
  app.put("/api/follows/:userId", auth, regional, r.change);
  app.delete("/api/follows/:userId", auth, regional, r.change);
  app.put("/api/blocks/:userId", auth, regional, r.change);
  app.delete("/api/blocks/:userId", auth, regional, r.change);
  app.post("/api/admin/auth/login", a.login);
  app.post("/api/admin/auth/logout", adminAuth, a.logout);
  app.put("/api/admin/users/:userId/certification", adminAuth, a.certify);
  app.delete("/api/admin/users/:userId/certification", adminAuth, a.uncertify);
  app.post("/api/admin/posting-window", adminAuth, a.openWindow);
  return app;
}
