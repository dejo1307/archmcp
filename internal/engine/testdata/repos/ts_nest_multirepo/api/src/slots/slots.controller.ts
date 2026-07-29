// The object argument form, which is what real NestJS code overwhelmingly uses.
// `version` must NOT become a path segment: NestJS versioning can be header- or
// media-type-based, and the decorator alone does not say which.
@Controller({
  path: "/v2/slots",
  version: VERSION_2024_09_04,
})
export class SlotsController {
  @Get("/available")
  async getAvailableSlots() {
    return this.svc.available();
  }

  @Post("/reserve")
  async reserveSlot() {
    return this.svc.reserve();
  }
}
