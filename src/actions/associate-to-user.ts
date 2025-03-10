import { z } from 'zod';
import type { ServerWebSocket } from '@/types';
import { authenticateRequest } from '@/services';
import { db } from '@/db';
import * as Schemas from '@/db/schemas';
import { eq } from 'drizzle-orm';

const KEY = 'associateToUser';

export const associateToUserPayloadSchema = z.object({
  user: z.object({
    id: z.string(),
    secret: z.string(),
  }),
});
export type AssociateToUserPayload = z.infer<typeof associateToUserPayloadSchema>;

export async function associateToUserAction(ws: ServerWebSocket, payload: AssociateToUserPayload) {
  if (ws.data.userId !== undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'existingUserAssociation',
    }));
    return;
  }

  const user = await authenticateRequest(ws, payload.user);
  if (user === undefined) {
    return;
  }
  if (user.connection !== null) {
    ws.send(JSON.stringify({
      success: false,
      code: 'existingConnectionAssociatedToUser',
    }));
    return;
  }

  await db.transaction(async (tx) => {
    await tx.update(Schemas.connections)
      .set({ userId: user.id })
      .where(eq(Schemas.connections.id, ws.data.connectionId));
    await tx.update(Schemas.users)
      .set({ connectionId: ws.data.connectionId })
      .where(eq(Schemas.users.id, user.id));
  });

  if (user.session !== null) {
    console.debug("SUBSCRIBED", {
      user: user.displayName,
      topic: `session:${user.session.id}`,
    });
    ws.subscribe(`session:${user.session.id}`);
    ws.data.sessionId = user.session.id;
  }

  ws.data.userId = user.id;
  ws.send(JSON.stringify({
    success: true,
    code: 'associateToUserResponse',
    activeSession: user.session,
  }));
}

export default {
  key: KEY,
  schema: associateToUserPayloadSchema,
  action: associateToUserAction,
} as const;
