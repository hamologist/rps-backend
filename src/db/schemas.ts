import { sqliteTable, text, type AnySQLiteColumn } from "drizzle-orm/sqlite-core";
import { type InferSelectModel, relations } from "drizzle-orm";

export const users = sqliteTable("users", {
  id: text("id").primaryKey(),
  displayName: text("display_name").notNull(),
  secret: text("secret").notNull(),
  sessionId: text("session_id").references((): AnySQLiteColumn => sessions.id, {
    onUpdate: 'cascade',
    onDelete: 'set null',
  }),
  connectionId: text("connection_id").references((): AnySQLiteColumn => connections.id, {
    onUpdate: 'cascade',
    onDelete: 'set null',
  }),
});
export type Users = InferSelectModel<typeof users>;
export const usersRelations = relations(users, ({ one }) => {
  return {
    session: one(sessions, {
      fields: [users.sessionId],
      references: [sessions.id],
    }),
    connection: one(connections, {
      fields: [users.connectionId],
      references: [connections.id],
    }),
  };
})

export const sessions = sqliteTable("sessions", {
  id: text("id").primaryKey(),
  playerOneId: text("player_one").references((): AnySQLiteColumn => users.id, {
    onUpdate: 'cascade',
    onDelete: 'cascade',
  }),
  playerTwoId: text("player_two").references((): AnySQLiteColumn => users.id, {
    onUpdate: 'cascade',
    onDelete: 'cascade',
  }),
  lastActionTimestamp: text("last_action_datetime").notNull(),
});
export type Sessions = InferSelectModel<typeof sessions>;
export const sessionsRelations = relations(sessions, ({ one }) => {
  return {
    playerOne: one(users, {
      fields: [sessions.playerOneId],
      references: [users.id],
    }),
    playerTwo: one(users, {
      fields: [sessions.playerTwoId],
      references: [users.id],
    }),
  };
});

export const connections = sqliteTable("connections", {
  id: text("id").primaryKey(),
  userId: text("user_id").references((): AnySQLiteColumn => users.id, {
    onUpdate: 'cascade',
    onDelete: 'cascade',
  }),
});
export const connectionsRelations = relations(connections, ({ one }) => {
  return {
    user: one(users, {
      fields: [connections.userId],
      references: [users.id],
    }),
  };
});
