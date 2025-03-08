import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { eq } from 'drizzle-orm';
import { retrieveSession } from '@/services';

const KEY = 'leaveSession';

export async function leaveSessionAction(ws: ServerWebSocket) {
  const session = await retrieveSession(ws, true);
  if (session === undefined) {
    if (ws.data.sessionId !== undefined) {
      ws.unsubscribe(`session:${ws.data.sessionId}`);
      ws.send(JSON.stringify({
        success: true,
        code: 'leaveSessionResponse',
        session: { id: ws.data.sessionId },
      }));
    }
    ws.data.sessionId = undefined;

    return;
  }

  await db.transaction(async (tx) => {
    if (session.playerOneId !== null) {
      await tx.update(Schemas.users)
        .set({ sessionId: null })
        .where(eq(Schemas.users.id, session.playerOneId));
    }

    if (session.playerTwoId !== null) {
      await tx.update(Schemas.users)
        .set({ sessionId: null })
        .where(eq(Schemas.users.id, session.playerTwoId));
    }
    await tx.delete(Schemas.sessions)
      .where(eq(Schemas.sessions.id, session.id));
  });

  ws.data.sessionId = undefined;
  ws.send(JSON.stringify({
    success: true,
    code: 'leaveSessionResponse',
    session: { id: session.id },
  }));
  ws.publish(`session:${session.id}`, JSON.stringify({
    code: 'sessionTerminated',
    sessionId: session.id,
  }));
  ws.unsubscribe(`session:${session.id}`);
}

export default {
  key: KEY,
  action: leaveSessionAction,
} as const;
