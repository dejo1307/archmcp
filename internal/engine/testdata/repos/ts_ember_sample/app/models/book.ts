import Model, { attr, belongsTo, hasMany } from '@ember-data/model';

export default class Book extends Model {
  @attr('duration') readTime;
  @belongsTo('author', { async: false, inverse: null }) author;
  @hasMany('review', { async: true, inverse: 'book' }) reviews;
}
