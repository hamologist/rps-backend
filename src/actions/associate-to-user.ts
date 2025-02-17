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

  const userUpdate = await db.transaction(async (tx) => {
    await tx.update(Schemas.connections)
      .set({ userId: user.id })
      .where(eq(Schemas.connections.id, ws.data.connectionId));
    const userUpdate = await tx.update(Schemas.users)
      .set({ connectionId: ws.data.connectionId })
      .where(eq(Schemas.users.id, user.id))
      .returning();

    if (ws.data.user !== undefined) {
      await tx.update(Schemas.users)
        .set({ connectionId: null })
        .where(eq(Schemas.users.id, ws.data.user.id));
    }

    return userUpdate[0]
  });

  if (user.session !== null) {
    ws.subscribe(`session:${user.session.id}`);
    ws.data.session = user.session;
  }

  ws.data.user = userUpdate;
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
