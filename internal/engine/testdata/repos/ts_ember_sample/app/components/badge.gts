import Component from '@glimmer/component';

export default class Badge extends Component {
  get tone() {
    return 'neutral';
  }

  <template>
    <span class="badge badge--{{this.tone}}">{{yield}}</span>
  </template>
}
