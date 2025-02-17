import { z } from 'zod';
import { getRandomValues } from 'crypto';
import * as Schemas from '@/db/schemas';
import { db } from '@/db';
import type { ServerWebSocket } from '@/types';

const KEY = 'createUser';

export const createUserPayloadSchema = z.object({
  displayName: z.string().min(1),
});
export type CreateUserPayload = z.infer<typeof createUserPayloadSchema>;

export async function createUserAction(ws: ServerWebSocket, payload: CreateUserPayload) {
  const userId = Bun.randomUUIDv7();
  const secret = getRandomValues(new Uint8Array(10))
    .reduce((acc, value) => acc + ("0" + value.toString(16)).slice(-2), "");

  const user = await db.transaction(async (tx) => {
    const user = await tx.insert(Schemas.users).values({
      id: userId,
      displayName: payload.displayName,
      secret,
      connectionId: ws.data.connectionId,
    }).returning();
    await tx.update(Schemas.connections).set({
      id: ws.data.connectionId,
      userId,
    });

    return user[0];
  });

  ws.data.user = user;
  ws.send(JSON.stringify({
    success: true,
    code: 'createUserResponse',
    user: {
      id: userId,
      secret,
    }
  }));
}

export default {
  key: KEY,
  schema: createUserPayloadSchema,
  action: createUserAction,
} as const;
