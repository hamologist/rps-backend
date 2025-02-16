import { drizzle } from "drizzle-orm/bun-sqlite";
import { Database } from "bun:sqlite";

const sqlite = new Database("db.sqlite");
export const db = drizzle(sqlite);

export class SelectMissingResultError extends Error {}

export function takeUniqueOrThrow<T extends any[]>(values: T): T[number] {
  if (values.length === 0) {
    throw new SelectMissingResultError();
  }
  return values[0];
}
