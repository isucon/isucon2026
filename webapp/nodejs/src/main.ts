import { serve } from "@hono/node-server";
import { configFromEnv, createPool } from "./db.js";
import { createApp } from "./server.js";
const config = configFromEnv();
const db = createPool(config);
await db.query("SELECT 1");
const app = createApp(db, config.region);
serve({ fetch: app.fetch, port: 8080 });
