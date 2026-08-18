import { blockId } from "@/lib/site-blocks";

export function useSite(raw: string): string {
  return blockId(raw);
}
