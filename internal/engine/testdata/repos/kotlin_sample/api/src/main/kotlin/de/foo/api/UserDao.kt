package de.foo.api

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.Query

// Room entity -> storage fact (storage_kind=entity), emitted when the project is
// detected as Android (AndroidManifest.xml present).
@Entity
data class UserEntity(val id: Int, val name: String)

// Room DAO. Each @Query/@Insert method is a direct DB I/O leaf, so the extractor
// tags it io_direct/performs_io (v57); the interface itself becomes a storage
// fact (storage_kind=dao).
@Dao
interface UserDao {
    @Query("SELECT * FROM UserEntity")
    fun getAll(): List<UserEntity>

    @Insert
    fun insertAll(users: List<UserEntity>)
}
