import { renderSite } from "@/components/sites/chabad-website";
import { useSite } from "@/hooks/use-site";

export function Page(): string {
  return renderSite({ id: useSite("Home"), kind: "page" });
}
