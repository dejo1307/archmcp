package de.foo.app

import org.junit.Test
import kotlin.test.assertEquals

// This file lives in the src/test source set, so its enclosing module fact is
// tagged module_role=test (v58) and excluded from the production population by
// package-metrics. It is also skipped by source-root/package-index detection so
// it never shadows the production de.foo.app package.
class HomeViewModelTest {
    @Test
    fun fibonacci() {
        assertEquals(5, fib(5))
    }
}
