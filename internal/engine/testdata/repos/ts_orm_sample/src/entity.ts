import { Entity, Column, PrimaryGeneratedColumn } from "typeorm";

// TypeORM: a decorated class. The decorator names the physical table.
@Entity("users")
export class User {
  @PrimaryGeneratedColumn()
  id: number;

  @Column()
  email: string;
}

// TypeORM: no table argument — the table name defaults to the class name.
@Entity()
export class Session {
  @PrimaryGeneratedColumn()
  id: number;
}

// NOT an entity: an ordinary class in the same file must emit no storage fact.
export class UserPresenter {
  present(u: User): string {
    return u.email;
  }
}
