import { drizzle } from "drizzle-orm/bun-sqlite";
import { Database } from "bun:sqlite";
import * as Schemas from "@/db/schemas";

const sqlite = new Database("db.sqlite");
export const db = drizzle(sqlite, { schema: Schemas });

export class MissingResultError extends Error {}

export function throwOnMissing<T>(value: T | undefined): T {
  if (value === undefined) {
    throw new MissingResultError();
  }

  return value;
}
