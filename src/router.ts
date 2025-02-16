import { z, ZodError } from 'zod';
import CreateUserAction from '@/actions/create-user';
import RetrieveUserAction from '@/actions/retrieve-user';
import type { ServerWebSocket } from '@/types';

export const routableMessageSchema = z.object({
  action: z.string(),
  payload: z.unknown(),
});
export type RoutableMessage = z.infer<typeof routableMessageSchema>;

export async function routeAction(ws: ServerWebSocket, routableMessage: RoutableMessage) {
  let routed: () => Promise<void>;
  try {
    switch(routableMessage.action) {
      case CreateUserAction.key: {
        const payload = CreateUserAction.schema.parse(routableMessage.payload);
        routed = () => CreateUserAction.action(ws, payload);
        break;
      }
      case RetrieveUserAction.key: {
        const payload = RetrieveUserAction.schema.parse(routableMessage.payload);
        routed = () => RetrieveUserAction.action(ws, payload);
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

    throw error;
  }

  return routed();
}
