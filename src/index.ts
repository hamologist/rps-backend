import { z } from 'zod';

const env = z.object({
  HOST: z.string(),
  PORT: z.coerce.number(),
}).parse(process.env);

type WebSocketData = {
  connectionId: string;
};

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
      ws.send(`Your connectionId is: ${ws.data.connectionId}`);
    },
  },
  port: env.PORT,
  hostname: env.HOST,
});

console.debug(`Listening on ${server.hostname}:${server.port}`);
