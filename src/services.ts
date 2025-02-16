import { eq } from 'drizzle-orm';
import * as Schemas from '@/db/schema';
import { db, takeUniqueOrThrow, SelectMissingResultError } from '@/db';
import type { ServerWebSocket, UserPayload } from '@/types';

class InvalidUserSecretError extends Error {}

export async function authenticateRequest(ws: ServerWebSocket, userPayload: UserPayload) {
  let user: Schemas.Users | undefined;
  try {
    user = await authenticateUser(userPayload);
  } catch (error) {
    if (error instanceof SelectMissingResultError) {
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
  const user = await db.select()
    .from(Schemas.users)
    .where(eq(Schemas.users.id, userPayload.id))
    .then(takeUniqueOrThrow);

  if(userPayload.secret !== user.secret) {
    throw new InvalidUserSecretError();
  }

  return user;
}
