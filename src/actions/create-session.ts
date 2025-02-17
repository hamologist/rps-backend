import { z } from 'zod';
import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { eq } from 'drizzle-orm';

const KEY = 'createSession';

export const createSessionPayloadSchema = z.undefined();
export type CreateSessionPayload = z.infer<typeof createSessionPayloadSchema>;

export async function createSessionAction(ws: ServerWebSocket, payload?: CreateSessionPayload) {
  const user = ws.data.user;
  if (user === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'connectionNotAssociatedToUser',
    }));
    return;
  }

  const sessionId = Bun.randomUUIDv7();
  const session = await db.transaction(async (tx) => {
    const session = await tx.insert(Schemas.sessions).values({
      id: sessionId,
      playerOneId: user.id,
    }).returning();
    await tx.update(Schemas.users)
      .set({ sessionId })
      .where(eq(Schemas.users.id, user.id));

    return session[0];
  });
  ws.data.session = session;
  ws.subscribe(`session:${sessionId}`);
  ws.send(JSON.stringify({
    success: true,
    code: 'createSessionResponse',
    session: { id: sessionId },
  }));
}

export default {
  key: KEY,
  schema: createSessionPayloadSchema,
  action: createSessionAction,
} as const;
