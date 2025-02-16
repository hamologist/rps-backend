import { z } from 'zod';
import * as Schemas from '@/db/schema';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';
import { authenticateRequest } from '@/services';

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

  const id = Bun.randomUUIDv7();
  await db.insert(Schemas.sessions).values({
    id,
    playerOne: user.id,
  });
  ws.send(JSON.stringify({
    success: true,
    code: 'createSessionResponse',
    session: { id },
  }));
}

export default {
  key: KEY,
  schema: createSessionPayloadSchema,
  action: createSessionAction,
} as const;
