import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { eq } from 'drizzle-orm';

const KEY = 'createSession';

export async function createSessionAction(ws: ServerWebSocket) {
  const user = ws.data.user;
  if (user === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'connectionNotAssociatedToUser',
    }));
    return;
  }

  const sessionId = Bun.randomUUIDv7();
  const { session, userUpdate } = await db.transaction(async (tx) => {
    const session = await tx.insert(Schemas.sessions).values({
      id: sessionId,
      playerOneId: user.id,
      lastActionTimestamp: new Date().toISOString(),
    }).returning();
    const userUpdate = await tx.update(Schemas.users)
      .set({ sessionId })
      .where(eq(Schemas.users.id, user.id))
      .returning();

    if (user.sessionId !== null) {
      await tx.delete(Schemas.sessions)
        .where(eq(Schemas.sessions.id, user.sessionId));
    }

    return {
      session: session[0],
      userUpdate: userUpdate[0],
    };
  });
  ws.data.session = session;
  ws.data.user = userUpdate;
  ws.subscribe(`session:${sessionId}`);
  ws.send(JSON.stringify({
    success: true,
    code: 'createSessionResponse',
    session: { id: sessionId },
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
  action: createSessionAction,
} as const;
