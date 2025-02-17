import { sqliteTable, text } from "drizzle-orm/sqlite-core";
import { type InferSelectModel, relations } from "drizzle-orm";

export const users = sqliteTable("users", {
  id: text("id").primaryKey(),
  displayName: text("display_name").notNull(),
  secret: text("secret").notNull(),
});
export type Users = InferSelectModel<typeof users>;
export const usersRelations = relations(users, ({ many }) => {
  return { usersToSessions: many(usersToSessions) };
})

export const sessions = sqliteTable("sessions", {
  id: text("id").primaryKey(),
  playerOneId: text("player_one").references(() => users.id, {
    onUpdate: 'cascade',
    onDelete: 'cascade',
  }),
  playerTwoId: text("player_two").references(() => users.id, {
    onUpdate: 'cascade',
    onDelete: 'cascade',
  }),
});
export type Sessions = InferSelectModel<typeof sessions>;
export const sessionsRelations = relations(sessions, ({ one, many }) => {
  return {
    usersToSessions: many(usersToSessions),
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

export const usersToSessions = sqliteTable("users_to_sessions", {
  userId: text("userId")
    .notNull()
    .references(() => users.id, {
      onDelete: 'cascade',
      onUpdate: 'cascade',
    }),
  sessionId: text("sessionId")
    .notNull()
    .references(() => sessions.id, {
      onDelete: 'cascade',
      onUpdate: 'cascade',
    }),
});
export const usersToSessionsRelations = relations(usersToSessions, ({ one }) => {
  return {
    user: one(users, {
      fields: [usersToSessions.userId],
      references: [users.id],
    }),
    session: one(sessions, {
      fields: [usersToSessions.sessionId],
      references: [sessions.id],
    }),
  };
})
