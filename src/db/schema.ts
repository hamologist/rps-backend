import { sqliteTable, text } from "drizzle-orm/sqlite-core";
import { type InferSelectModel } from "drizzle-orm";

export const users = sqliteTable("users", {
  id: text("id").primaryKey(),
  displayName: text("display_name").notNull(),
  secret: text("secret").notNull(),
});
export type Users = InferSelectModel<typeof users>;

export const sessions = sqliteTable("sessions", {
  id: text("id").primaryKey(),
  playerOne: text("player_one").references(() => users.id),
  playerTwo: text("player_two").references(() => users.id),
});
export type Sessions = InferSelectModel<typeof sessions>;
