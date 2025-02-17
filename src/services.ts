import { eq } from 'drizzle-orm';
import * as Schemas from '@/db/schemas';
import { db, throwOnMissing, MissingResultError } from '@/db';
import type { ServerWebSocket, UserPayload } from '@/types';

class InvalidUserSecretError extends Error {}

export type FullUser = Awaited<ReturnType<typeof authenticateUser>>

export async function authenticateRequest(ws: ServerWebSocket, userPayload: UserPayload) {
  let user: FullUser | undefined;
  try {
    user = await authenticateUser(userPayload);
  } catch (error) {
    if (error instanceof MissingResultError) {
      ws.send(JSON.stringify({
        success: false,
        code: 'userNotFound',
        user: { id: userPayload.id },
      }));
    } else if (error instanceof InvalidUserSecretError) {
      ws.send(JSON.stringify({
        success: false,
        code: 'invalidUserSecret',
        user: { id: userPayload.id },
      }));
    } else {
      ws.send(JSON.stringify({
        success: false,
        code: 'serverError',
      }));
    }
  }

  return user;
}

export async function authenticateUser(userPayload: UserPayload) {
  const user = await db.query.users.findFirst({
    with: {
      session: true,
      connection: true,
    },
    where: eq(Schemas.users.id, userPayload.id),
  }).then(throwOnMissing)

  if(userPayload.secret !== user.secret) {
    throw new InvalidUserSecretError();
  }

  return user;
}
