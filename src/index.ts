import type { WebSocketData } from '@/types';
import { z } from 'zod';
import { type RoutableMessage, routableMessageSchema, routeAction } from '@/router';

const env = z.object({
  HOST: z.string(),
  PORT: z.coerce.number(),
}).parse(process.env);

const server = Bun.serve<WebSocketData>({
  fetch(req, server) {
    const url = new URL(req.url);
    if (url.pathname === "/websocket") {
      const upgraded = server.upgrade(req, {
        data: {
          connectionId: Bun.randomUUIDv7(),
        },
      });
      if (!upgraded) {
        return new Response("Upgrade failed", { status: 400 });
      }
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
  },
  port: env.PORT,
  hostname: env.HOST,
});

console.debug(`Listening on ${server.hostname}:${server.port}`);
