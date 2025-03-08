import { z, ZodError } from 'zod';
import CreateUserAction from '@/actions/create-user';
import AssociateToUserAction from '@/actions/associate-to-user';
import CreateSessionAction from '@/actions/create-session';
import JoinSessionAction from '@/actions/join-session';
import LeaveSessionAction from '@/actions/leave-session';
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
      case AssociateToUserAction.key: {
        const payload = AssociateToUserAction.schema.parse(routableMessage.payload);
        routed = () => AssociateToUserAction.action(ws, payload);
        break;
      }
      case CreateSessionAction.key: {
        routed = () => CreateSessionAction.action(ws);
        break;
      }
      case JoinSessionAction.key: {
        const payload = JoinSessionAction.schema.parse(routableMessage.payload);
        routed = () => JoinSessionAction.action(ws, payload);
        break;
      }
      case LeaveSessionAction.key: {
        routed = () => LeaveSessionAction.action(ws);
        break;
      }
      default: {
        ws.send(JSON.stringify({
          success: false,
          code: 'unknownAction',
          action: routableMessage.action,
        }));
        return;
      }
    }
  } catch (error) {
    if (error instanceof ZodError) {
      ws.send(JSON.stringify({
        success: false,
        code: 'actionParseError',
        action: routableMessage.action,
        error: error.issues,
      }));
      return;
    }

    throw error;
  }

  return routed();
}
