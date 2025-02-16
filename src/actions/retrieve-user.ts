import { z } from 'zod';
import type { ServerWebSocket } from '@/types';
import { authenticateRequest } from '@/services';

const KEY = 'retrieveUser';

export const retrieveUserPayloadSchema = z.object({
  user: z.object({
    id: z.string(),
    secret: z.string(),
  }),
});
export type RetrieveUserPayload = z.infer<typeof retrieveUserPayloadSchema>;

export async function retrieveUserAction(ws: ServerWebSocket, payload: RetrieveUserPayload) {
  const user = await authenticateRequest(ws, payload.user);

  if (user !== undefined) {
    ws.send(JSON.stringify({
      success: true,
      code: 'retrieveUserResponse',
      user,
    }));
  }
}

export default {
  key: KEY,
  schema: retrieveUserPayloadSchema,
  action: retrieveUserAction,
} as const;
