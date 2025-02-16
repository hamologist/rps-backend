import { z } from 'zod';
import { eq } from 'drizzle-orm';
import * as Schemas from '@/db/schema';
import { db, takeUniqueOrThrow, SelectMissingResultError } from '@/db';
import type { ServerWebSocket } from '@/types';

const KEY = 'retrieveUser';

export const retrieveUserPayloadSchema = z.object({
  id: z.string(),
  secret: z.string(),
});
export type RetrieveUserPayload = z.infer<typeof retrieveUserPayloadSchema>;

export async function retrieveUserAction(ws: ServerWebSocket, payload: RetrieveUserPayload) {
  let user: Schemas.Users;
  try {
    user = await db.select()
      .from(Schemas.users)
      .where(eq(Schemas.users.id, payload.id))
      .then(takeUniqueOrThrow);
  } catch(error) {
    if (error instanceof SelectMissingResultError) {
      ws.send(JSON.stringify({
        success: false,
        code: 'user-not-found',
        user: { id: payload.id },
      }));
      return;
    }

    ws.send(JSON.stringify({
      success: false,
      code: 'server-error',
    }));
    return;
  }

  if(payload.secret !== user.secret) {
    ws.send(JSON.stringify({
      success: false,
      code: 'invalid-user-secret',
      user: { id: payload.id },
    }))
  }
  ws.send(JSON.stringify({
    success: true,
    code: 'retrieve-user-response',
    user,
  }));
}

export default {
  key: KEY,
  schema: retrieveUserPayloadSchema,
  action: retrieveUserAction,
} as const;
