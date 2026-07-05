package de.foo.app

import io.reactivex.rxjava3.core.Single
import de.foo.api.ApiService
import de.foo.api.User
import de.foo.api.formatUser

// Cross-module imports (de.foo.api.*) must resolve to the api module's own
// directory, not collapse onto the app source root (v58). The bare call to the
// imported top-level function `formatUser` produces a resolved call edge (v53).
class HomeViewModel(
    private val service: ApiService,
) {

    fun render(user: User): String {
        return formatUser(user)
    }

    // Callable references (`::foo`, used as a value / passed to .map) must be
    // credited as short-name call edges so a function referenced only by
    // reference is not mis-reported as a dead orphan (v55).
    fun wire() {
        register(onClick = ::doNothing)
        listOf(1, 2, 3).map(::square)
    }

    fun doNothing() {}

    fun square(x: Int): Int = x * x

    // Arity-aware recursion (v56): the 1-arg overload delegates, inside a loop,
    // to the 2-arg overload. It shares the name but not the parameter count, so
    // it must NOT be flagged recursive_self (the Conductor updateItem case).
    fun updateItem(x: Int) {
        for (i in 0 until 3) {
            updateItem(i, x)
        }
    }

    fun updateItem(i: Int, x: Int) {}

    // Super-delegation (v57): an override calls super.<self>() and then a
    // same-name, same-arity overload declared in a parent (invisible here). The
    // super call marks the sibling call as delegation, not recursion.
    override fun onChangeEnded(handler: Handler, type: Type) {
        super.onChangeEnded(handler, type)
        onChangeEnded(fallbackHandler(), type)
    }

    // RxJava reactive chain (v56): a Single.flatMap { ... .map { } } chain runs
    // once per emission, not per collection element, so its map/flatMap must NOT
    // inflate loop_depth into a false O(n^2)/O(n^3).
    fun getInvitable(): Single<List<Int>> {
        return service.stream()
            .flatMap { g -> service.more().map { toList(it) } }
            .applySchedulers()
    }
}

// Genuine recursion (v56 control): a same-name, same-arity self-call is still
// flagged recursive_self, proving the arity check does not suppress real cases.
fun fib(n: Int): Int {
    if (n < 2) return n
    return fib(n - 1) + fib(n - 2)
}
