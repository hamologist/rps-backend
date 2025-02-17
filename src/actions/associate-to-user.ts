import { z } from 'zod';
import type { ServerWebSocket } from '@/types';
import { authenticateRequest, retrieveUserSessions } from '@/services';

const KEY = 'associateToUser';

export const associateToUserPayloadSchema = z.object({
  user: z.object({
    id: z.string(),
    secret: z.string(),
  }),
});
export type AssociateToUserPayload = z.infer<typeof associateToUserPayloadSchema>;

export async function associateToUserAction(ws: ServerWebSocket, payload: AssociateToUserPayload) {
  const user = await authenticateRequest(ws, payload.user);
  if (user === undefined) {
    return;
  }

  const sessions = await retrieveUserSessions(user.id);
  for (const session of sessions) {
    ws.subscribe(`session:${session.id}`)
  }
  ws.send(JSON.stringify({
    success: true,
    code: 'associateToUserResponse',
    sessions: sessions.map(session => session.id),
  }));
}

export default {
  key: KEY,
  schema: associateToUserPayloadSchema,
  action: associateToUserAction,
} as const;
