// v105 — bounded-loop discounting (GAP-CP-01). C/C++ joins the Go/Python/TS/Kotlin/Java
// convention: loop_depth counts every loop, scaling_loop_depth counts only input-scaling
// loops (the Big-O exponent), and calls_in_scaling_loop is the N+1-candidate subset
// (calls inside a loop that repeats a non-constant number of times). Pins all three
// loop classes in the golden facts. Parsed with tree-sitter-cpp (the getdp grammar).
void step();
void db_query();

// A literal-bounded for runs a fixed number of times: loop_depth=1,
// scaling_loop_depth=0. Its call is recorded in calls_in_loop but is NOT an N+1
// candidate, so calls_in_scaling_loop is present-but-empty.
void constant_loop() {
    for (int i = 0; i < 3; i++) {
        step();
    }
}

// A data-derived for scales: loop_depth=1, scaling_loop_depth=1; the per-iteration
// call is an N+1 candidate (calls_in_scaling_loop retains it).
void scaling_loop(int n) {
    for (int i = 0; i < n; i++) {
        db_query();
    }
}

// An infinite loop is discounted from the exponent (scaling_loop_depth=0) but still
// repeats, so its query stays an N+1 candidate: calls_in_scaling_loop keeps db_query.
void retry_loop() {
    for (;;) {
        db_query();
    }
}

// A range-for over a braced init-list iterates a fixed count → constant.
void constant_range() {
    for (int x : {1, 2, 3}) {
        step();
    }
}

// Constant outer, scaling inner: loop_depth=2 but scaling_loop_depth=1 — only the inner
// input-scaling loop contributes to the exponent.
void nested(int n) {
    for (int i = 0; i < 4; i++) {
        for (int j = 0; j < n; j++) {
            db_query();
        }
    }
}
