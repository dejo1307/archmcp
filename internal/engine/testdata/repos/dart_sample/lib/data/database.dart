import 'package:drift/drift.dart';

// `Table` is also a Flutter WIDGET (material's Table lays out a grid), so this is only
// a storage fact because the file imports drift. The name drift derives is the
// lower_snake_case of the class.
class TodoItems extends Table {
  IntColumn get id => integer().autoIncrement()();
  TextColumn get title => text()();
}

class ArchivedTodos extends Table {
  @override
  String get tableName => 'archive_v2';
  IntColumn get id => integer()();
}
