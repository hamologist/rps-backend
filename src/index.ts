import type { ServerWebSocket as BaseServerWebSocket } from 'bun';
import { z, ZodError } from 'zod';

type WebSocketData = {
  connectionId: string;
};

type ServerWebSocket = BaseServerWebSocket<WebSocketData>;

const env = z.object({
  HOST: z.string(),
  PORT: z.coerce.number(),
}).parse(process.env);

const routableMessageSchema = z.object({
  action: z.string(),
  payload: z.unknown(),
});
type RoutableMessage = z.infer<typeof routableMessageSchema>;

const echoActionSchema = z.object({
  action: z.literal('echo'),
  payload: z.object({
    input: z.string(),
  }),
});
type EchoAction = z.infer<typeof echoActionSchema>;

function echoAction(ws: ServerWebSocket, payload: EchoAction['payload']) {
  ws.send(JSON.stringify({
    success: true,
    code: 'echo-response',
    message: payload.input,
  }));
}

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
          code: 'message-parse-error',
          error: error instanceof Error ? error.message : error,
        }));
        return;
      }

      try {
        switch (routableMessage.action) {
          case 'echo': {
            echoAction(ws, echoActionSchema.parse(routableMessage).payload);
            break;
          }
          default: {
            ws.send(JSON.stringify({
              success: false,
              code: 'unknown-action',
              action: routableMessage.action,
            }));
            return;
          }
        }
      } catch (error) {
        if (error instanceof ZodError) {
          ws.send(JSON.stringify({
            success: false,
            code: 'action-parse-error',
            action: routableMessage.action,
            error: error.issues,
          }));
          return;
        }

        ws.send(JSON.stringify({
          success: false,
          code: 'server-error-in-action',
          action: routableMessage.action,
          error,
        }));
        return
      }
    },
  },
  port: env.PORT,
  hostname: env.HOST,
});

console.debug(`Listening on ${server.hostname}:${server.port}`);
