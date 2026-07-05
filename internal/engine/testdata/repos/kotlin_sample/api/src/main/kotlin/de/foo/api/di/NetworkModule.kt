package de.foo.api.di

import dagger.Binds
import dagger.Component
import dagger.Module
import dagger.Provides
import de.foo.api.ApiService

interface UserRepository
class UserRepositoryImpl : UserRepository

// @Module class -> di_module=true (v60). Its @Provides / @Binds methods are
// invoked reflectively by the DI container, so each is tagged di_provider=true
// (v52) and excluded from orphan reporting.
@Module
class NetworkModule {
    @Provides
    fun provideApiService(): ApiService = throw NotImplementedError()

    @Binds
    fun bindUserRepository(impl: UserRepositoryImpl): UserRepository = impl
}

// @Component interface -> di_component=true (v60); a Dagger component is DI
// wiring, not a Spring component (disambiguated by interface-vs-class).
@Component(modules = [NetworkModule::class])
interface AppComponent {
    fun apiService(): ApiService
}
