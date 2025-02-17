import type { WebSocketData } from '@/types';
import { z } from 'zod';
import { type RoutableMessage, routableMessageSchema, routeAction } from '@/router';
import { db } from '@/db';
import * as Schemas from '@/db/schemas'
import { eq } from 'drizzle-orm';

const env = z.object({
  HOST: z.string(),
  PORT: z.coerce.number(),
}).parse(process.env);

await db.delete(Schemas.connections);

const server = Bun.serve<WebSocketData>({
  async fetch(req, server) {
    const url = new URL(req.url);
    if (url.pathname === "/websocket") {
      const connectionId = Bun.randomUUIDv7();
      const upgraded = server.upgrade<WebSocketData>(req, {
        data: { connectionId },
      });
      if (!upgraded) {
        return new Response("Upgrade failed", { status: 400 });
      }

      await db.insert(Schemas.connections).values({ id: connectionId });
    }
    return new Response("Hello World");
  },
  websocket: {
    async message(ws, message) {
      let routableMessage: RoutableMessage;
      try {
        routableMessage = routableMessageSchema.parse(JSON.parse(String(message)));
      } catch (error) {
        ws.send(JSON.stringify({
          success: false,
          code: 'messageParseError',
          error: error instanceof Error ? error.message : error,
        }));
        return;
      }

      try {
        await routeAction(ws, routableMessage);
      } catch (error) {
        ws.send(JSON.stringify({
          success: false,
          code: 'unhandeledServerError',
          action: routableMessage.action,
          error,
        }));
        console.error("SERVER ERROR:", error);
        return
      }
    },
    async close(ws) {
      await db
        .delete(Schemas.connections)
        .where(eq(Schemas.connections.id, ws.data.connectionId));
    }
  },
  port: env.PORT,
  hostname: env.HOST,
});

console.debug(`Listening on ${server.hostname}:${server.port}`);
