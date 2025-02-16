import type { ServerWebSocket as BaseServerWebSocket } from 'bun';

export type WebSocketData = {
  connectionId: string;
};

export type ServerWebSocket = BaseServerWebSocket<WebSocketData>;

export type UserPayload = {
  id: string;
  secret: string;
};
