import Service from '@ember/service';

export default class LibraryService extends Service {
  checkedOut = 0;

  checkOut() {
    this.checkedOut += 1;
  }
}
