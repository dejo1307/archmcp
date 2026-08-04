import buildRoutes from 'ember-engines/routes';

export default buildRoutes(function () {
  this.route('cart', function () {
    this.route('checkout');
  });
});
