import Model, { belongsTo, hasMany } from '@ember-data/model';

export default class Book extends Model {
  @belongsTo('author', { async: false, inverse: null }) author;
  @hasMany('review', { async: true, inverse: 'book' }) reviews;
}
