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
  const id = Bun.randomUUIDv7();
  const secret = getRandomValues(new Uint8Array(10))
    .reduce((acc, value) => acc + ("0" + value.toString(16)).slice(-2), "");

  await db.insert(Schemas.users).values({
    id,
    displayName: payload.displayName,
    secret,
  });

  ws.send(JSON.stringify({
    success: true,
    code: 'createUserResponse',
    user: {
      id,
      secret,
    }
  }));
}

export default {
  key: KEY,
  schema: createUserPayloadSchema,
  action: createUserAction,
} as const;
