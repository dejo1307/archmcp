package com.example.compute;

// v104 — bounded-loop discounting (GAP-JV-01). Java joins the Go/Python/TS/Kotlin
// convention: loop_depth counts every loop, scaling_loop_depth counts only
// input-scaling loops (the Big-O exponent), and calls_in_scaling_loop is the
// N+1-candidate subset (calls inside a loop that repeats a non-constant number of
// times). This class pins all three loop classes in the golden facts.
public class LoopMetrics {

    // A literal-bounded for runs a fixed number of times: loop_depth=1,
    // scaling_loop_depth=0. Its resolved same-class call is recorded in calls_in_loop
    // but is NOT an N+1 candidate, so calls_in_scaling_loop is present-but-empty.
    public void constantLoop() {
        for (int i = 0; i < 3; i++) {
            step();
        }
    }

    // A data-derived for-each scales: loop_depth=1, scaling_loop_depth=1. The
    // per-iteration repo call is an N+1 candidate (calls_in_scaling_loop retains it).
    public void scalingLoop(java.util.List<String> ids) {
        for (String id : ids) {
            repo.findById(id);
        }
    }

    // An infinite loop is discounted from the Big-O exponent (scaling_loop_depth=0) but
    // still repeats, so its query stays an N+1 candidate: calls_in_scaling_loop retains
    // repo.poll even though the loop adds no factor of n.
    public void retryLoop() {
        while (true) {
            repo.poll();
        }
    }

    // Constant outer, scaling inner: loop_depth=2 but scaling_loop_depth=1 — only the
    // inner input-scaling loop contributes to the exponent.
    public void nested(java.util.List<String> ids) {
        for (int i = 0; i < 4; i++) {
            for (String id : ids) {
                repo.findById(id);
            }
        }
    }

    void step() {}
}
