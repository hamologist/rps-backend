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

export async function retrieveUser(ws: ServerWebSocket) {
  if (ws.data.userId === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'connectionNotAssociatedToUser',
    }));
    return;
  }

  const user = await db.query.users.findFirst({
    where: eq(Schemas.users.id, ws.data.userId),
  });
  if (user === undefined) {
    ws.data.userId = undefined;
    ws.send(JSON.stringify({
      success: false,
      code: 'userNoLongerInDB',
    }));
    return;
  }

  return user;
}

export async function retrieveSession(
  ws: ServerWebSocket,
  ignoreMissing: boolean = false
) {
  if (ws.data.sessionId === undefined) {
    ws.send(JSON.stringify({
      success: false,
      code: 'connectionNotAssociatedToSession',
    }));
    return;
  }

  const session = await db.query.sessions.findFirst({
    where: eq(Schemas.sessions.id, ws.data.sessionId),
  });
  if (session === undefined && !ignoreMissing) {
    ws.data.userId = undefined;
    ws.send(JSON.stringify({
      success: false,
      code: 'sessionNoLongerInDB',
    }));
  }

  return session;
}
