
import { z } from 'zod';
import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { eq } from 'drizzle-orm';

const KEY = 'joinSession';

export const joinSessionPayloadSchema = z.object({
  sessionId: z.string(),
});
export type JoinSessionPayload = z.infer<typeof joinSessionPayloadSchema>;

export async function joinSessionAction(ws: ServerWebSocket, payload: JoinSessionPayload) {
  const user = ws.data.user;
  if (user === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'connectionNotAssociatedToUser',
    }));
    return;
  }

  const session = await db.query.sessions.findFirst({
    where: eq(Schemas.sessions.id, payload.sessionId),
  });
  if (session === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'sessionNotFound',
      sessionId: payload.sessionId,
    }));
    return;
  } else if (session.playerTwoId !== null) {
    ws.send(JSON.stringify({
      success: false,
      code: 'sessionAlreadyHasMaxPlayers',
      sessionId: payload.sessionId,
    }));
    return;
  } else if (session.playerOneId === user.id) {
    ws.send(JSON.stringify({
      success: false,
      code: 'userAlreadyOnSession',
      sessionId: payload.sessionId,
    }));
    return;
  }

  const { sessionUpdate, userUpdate } = await db.transaction(async (tx) => {
    const sessionUpdate = await tx.update(Schemas.sessions)
      .set({
        playerTwoId: user.id,
        lastActionTimestamp: new Date().toISOString()
      })
      .where(eq(Schemas.sessions.id, session.id))
      .returning();
    const userUpdate = await tx.update(Schemas.users)
      .set({ sessionId: session.id })
      .where(eq(Schemas.users.id, user.id))
      .returning();

    if (user.sessionId !== null) {
      await tx.delete(Schemas.sessions)
        .where(eq(Schemas.sessions.id, user.sessionId));
    }

    return {
      sessionUpdate: sessionUpdate[0],
      userUpdate: userUpdate[0],
    };
  });

  ws.data.user = userUpdate;
  ws.data.session = sessionUpdate;
  ws.subscribe(`session:${sessionUpdate.id}`);
  ws.send(JSON.stringify({
    success: true,
    code: 'createSessionResponse',
    session: { id: sessionUpdate.id },
  }));
  ws.publish(`session:${sessionUpdate.id}`, JSON.stringify({
    code: 'sessionUpdate',
    sessionUpdate,
  }));

  if (user.sessionId !== null) {
    ws.publish(`session:${user.sessionId}`, JSON.stringify({
      code: 'sessionTerminated',
      sessionId: user.sessionId,
    }));
    ws.unsubscribe(`session:${user.sessionId}`);
  }
}

export default {
  key: KEY,
  schema: joinSessionPayloadSchema,
  action: joinSessionAction,
} as const;
