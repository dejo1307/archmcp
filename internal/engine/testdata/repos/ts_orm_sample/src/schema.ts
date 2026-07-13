import { pgTable, serial, text } from "drizzle-orm/pg-core";

// Drizzle: a table declared as an exported const.
export const orders = pgTable("orders", {
  id: serial("id").primaryKey(),
  sku: text("sku"),
});

// NOT a table: an ordinary exported const must emit no storage fact.
export const MAX_ORDERS = 100;
