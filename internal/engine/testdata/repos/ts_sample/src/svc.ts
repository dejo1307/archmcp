import { Repo } from "./repo";

// Service depends on Repo and exposes a list operation.
export class Service {
  constructor(private repo: Repo) {}

  list(): string[] {
    return this.repo.all();
  }
}
