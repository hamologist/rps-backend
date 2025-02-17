import { z } from 'zod';
import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { authenticateRequest } from '@/services';
import { eq } from 'drizzle-orm';

const KEY = 'createSession';

export const createSessionPayloadSchema = z.object({
  user: z.object({
    id: z.string(),
    secret: z.string(),
  }),
});
export type CreateSessionPayload = z.infer<typeof createSessionPayloadSchema>;

export async function createSessionAction(ws: ServerWebSocket, payload: CreateSessionPayload) {
  const user = await authenticateRequest(ws, payload.user);
  if (user === undefined) {
    return;
  }

  const sessionId = Bun.randomUUIDv7();
  await db.transaction(async (tx) => {
    await tx.insert(Schemas.sessions).values({
      id: sessionId,
      playerOneId: user.id,
    });
    await tx.update(Schemas.users)
      .set({ sessionId })
      .where(eq(Schemas.users.id, user.id));
  });
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
