// The bare-string argument form, plus a verb decorator with no argument at all —
// which serves the class path itself, not a child of it.
@Controller("/users")
export class UsersController {
  @Get()
  findAll() {
    return [];
  }

  @Patch("/:id/profile")
  updateProfile() {
    return { ok: true };
  }
}

// InversifyJS vocabulary: lowercase @controller, @httpGet-prefixed verbs. Kept in
// the same fixture so the golden pins that both frameworks compose, and that the two
// vocabularies stay separate — @httpGet inside the NestJS controller above would emit
// nothing, and @Get inside this one likewise.
@controller("/api/orders")
export class OrderController {
  @httpGet("/")
  list() {
    return [];
  }

  @httpPost("/:id/cancel")
  cancel() {
    return { ok: true };
  }
}
