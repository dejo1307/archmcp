<?php
// v106 — bounded-loop discounting (GAP-PH-01). PHP joins the Go/Python/TS/Kotlin/Java
// convention: loop_depth counts every loop, scaling_loop_depth counts only input-scaling
// loops (the Big-O exponent), and calls_in_scaling_loop is the N+1-candidate subset
// (calls inside a loop that repeats a non-constant number of times). Pins all three
// loop classes in the golden facts.

function acme_step() {}

// A literal-bounded for runs a fixed number of times: loop_depth=1,
// scaling_loop_depth=0. Its call is recorded in calls_in_loop but is NOT an N+1
// candidate, so calls_in_scaling_loop is present-but-empty.
function acme_constant_loop() {
    for ($i = 0; $i < 3; $i++) {
        acme_step();
    }
}

// A data-derived foreach scales: loop_depth=1, scaling_loop_depth=1; the per-iteration
// call is an N+1 candidate (calls_in_scaling_loop retains it).
function acme_scaling_loop($rows) {
    foreach ($rows as $row) {
        acme_query($row);
    }
}

// An infinite loop is discounted from the exponent (scaling_loop_depth=0) but still
// repeats, so its query stays an N+1 candidate: calls_in_scaling_loop keeps acme_query.
function acme_retry_loop() {
    while (true) {
        acme_query(null);
    }
}

// A foreach over an array literal iterates a fixed count → constant.
function acme_constant_foreach() {
    foreach ([1, 2, 3] as $x) {
        acme_step();
    }
}

// Constant outer, scaling inner: loop_depth=2 but scaling_loop_depth=1 — only the inner
// input-scaling loop contributes to the exponent.
function acme_nested($rows) {
    for ($i = 0; $i < 4; $i++) {
        foreach ($rows as $row) {
            acme_query($row);
        }
    }
}
