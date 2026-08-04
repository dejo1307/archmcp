import { service } from '@ember/service';

export default class CatalogRoute {
  @service declare router: unknown;

  openAccount() {
    this.router.transitionTo('account');
  }

  libraryVia(owner: { lookup(k: string): unknown }) {
    return owner.lookup('service:library');
  }
}
