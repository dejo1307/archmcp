package de.foo.api

import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST

// Cross-module data model imported by the app module.
data class User(val id: Int, val name: String)

data class LoginRequest(val email: String, val password: String)

data class LoginResponse(val token: String)

// Retrofit endpoint interface. Each @GET/@POST method is a direct network I/O
// leaf, so the extractor tags it io_direct/performs_io (v57), and each `fun` is
// a member function -> SymbolMethod, not SymbolFunc (v52).
interface ApiService {
    @GET("/api/users/active")
    suspend fun getActiveUsers(): Response<List<User>>

    @POST("auth/login")
    suspend fun login(@Body request: LoginRequest): Response<LoginResponse>
}

// A top-level utility function declared in the api module and imported +
// called from the app module — exercises cross-module import resolution (v58)
// and the imported-top-level-function call edge (v53).
fun formatUser(user: User): String = "${user.id}:${user.name}"
