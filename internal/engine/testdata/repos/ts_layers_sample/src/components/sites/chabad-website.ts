export type SiteBlock = {
  id: string;
  kind: string;
};

export function renderSite(block: SiteBlock): string {
  return `${block.kind}:${block.id}`;
}
