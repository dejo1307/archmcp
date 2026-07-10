// Repo is the data-access layer used by Service.
export class Repo {
  all(): string[] {
    return [];
  }
}

// `while (true)` adds no factor of n but repeats, so the lookup stays an N+1 candidate.
export function getPath(id: string): void {
  while (true) {
    lookup(id);
  }
}

// Constant loop → calls_in_scaling_loop is emitted empty, not omitted.
export function seed(): void {
  for (const c of ["a", "b"]) {
    lookup(c);
  }
}

function lookup(id: string): void {}
