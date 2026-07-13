import { PrismaClient } from "@prisma/client";

const prisma = new PrismaClient();

// A repository WRAPPER around an ORM call. It invokes no network primitive, so it
// was never seeded io_direct — which is exactly why a per-iteration call to it was
// invisible to the performance analyzer. Seeding io_direct from the ORM call makes
// this performs_io, and the loop below becomes a detectable N+1.
export async function loadPostsFor(authorId: number) {
  return prisma.post.findMany({ where: { authorId } });
}

// The N+1: a call to the wrapper, once per iteration.
export async function loadFeed(authorIds: number[]) {
  const out = [];
  for (const id of authorIds) {
    const posts = await loadPostsFor(id);
    out.push(posts);
  }
  return out;
}

// A pure in-memory helper whose name reuses the CamelCase verbs. It must NOT be
// treated as I/O — this is the false-positive class the TS detector was narrowed to
// avoid (getFetchAllUpdate, updateState, findIndex, ...).
export function findIndexOfPost(posts: { id: number }[], id: number) {
  const seen = [];
  for (const p of posts) {
    seen.push(p.id === id);
  }
  return seen;
}
