import { sqliteTable, text } from "drizzle-orm/sqlite-core";
import { type InferSelectModel } from "drizzle-orm";

export const users = sqliteTable("users", {
  id: text("id").primaryKey(),
  displayName: text("display_name").notNull(),
  secret: text("secret").notNull(),
});
export type Users = InferSelectModel<typeof users>;
