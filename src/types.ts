import type { ServerWebSocket as BaseServerWebSocket } from 'bun';
import type { users, sessions } from '@/db/schemas';
import { type InferSelectModel } from "drizzle-orm"

export type WebSocketData = {
  connectionId: string;
  userId?: string;
  sessionId?: string;
};

export type ServerWebSocket = BaseServerWebSocket<WebSocketData>;

export type UserPayload = {
  id: string;
  secret: string;
};
