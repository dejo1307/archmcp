import EmberRouter from '@ember/routing/router';

export default class Router extends EmberRouter {}

Router.map(function () {
  this.route('catalog', function () {
    this.route('book', { path: '/:book_id' }, function () {
      this.route('reviews');
    });
  });
  this.route('account', { path: '/my-account' });
  this.mount('shop', { path: '/store' });
});
