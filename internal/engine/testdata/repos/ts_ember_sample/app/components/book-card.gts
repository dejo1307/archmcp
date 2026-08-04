import Component from '@glimmer/component';
import { service } from '@ember/service';
import Badge from 'shelf/components/badge';

export default class BookCard extends Component {
  @service declare library: unknown;

  get title() {
    return 'Untitled';
  }

  <template>
    <article class="book-card">
      <h2>{{this.title}}</h2>
      <Badge>{{@status}}</Badge>
    </article>
  </template>
}
