import { Repo } from "./repo";
import { Service } from "./svc";

// Entry point wires the Service to a Repo instance.
export function main(): string[] {
  const svc = new Service(new Repo());
  return svc.list();
}
