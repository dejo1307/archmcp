import { service } from '@ember/service';

export default class CatalogRoute {
  @service declare router: unknown;

  openAccount() {
    this.router.transitionTo('account');
  }
}
