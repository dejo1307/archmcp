// The precision control. This class carries a verb decorator but NOT a controller
// decorator, so it must contribute no route at all — @Get is far too generic a name
// to mint routes on its own. Its absence from the golden is the assertion.
@Injectable()
export class SlotsService {
  @Get("/not-a-route")
  available() {
    return [];
  }

  reserve() {
    return { ok: true };
  }
}
