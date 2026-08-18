// The layer violation this fixture pins: web-lib is the INNERMOST layer, and this
// import reaches up into web-components. Importing a type is still an import — the
// dependency edge is what a layer order is stated about.
import type { SiteBlock } from "@/components/sites/chabad-website";

export function blockId(raw: string): string {
  return raw.trim().toLowerCase();
}

export function emptyBlocks(): SiteBlock[] {
  return [];
}
