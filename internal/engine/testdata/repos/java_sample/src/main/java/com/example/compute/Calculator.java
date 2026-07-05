package com.example.compute;

// An abstract class (v58: abstractness A counts non-instantiable types; java_ast
// tags it with abstract=true). It also exercises the arity-aware recursion
// heuristics (v56/v57).
public abstract class Calculator {

    // v56 baseline — GENUINE recursion: fib(n-1)/fib(n-2) are same-name AND
    // same-arity (1) self-calls, so recursive_self=true is correctly flagged.
    public int fib(int n) {
        if (n < 2) return n;
        return fib(n - 1) + fib(n - 2);
    }

    // v56 — arity-aware: updateItem(x) (arity 1) calls updateItem(0, x) (arity 2),
    // a same-name DIFFERENT-arity overload. The arg count (2) != this method's
    // param count (1), so it is NOT mistaken for self-recursion.
    public void updateItem(Item x) {
        updateItem(0, x);
    }

    public void updateItem(int index, Item x) {
        // real work; no self-call.
    }

    public abstract void render();
}

// v57 — super-delegation: render() (arity 0) calls super.render() (marks the
// override as delegating) AND makes an arity-matched bare render() call. The
// arity match alone would read as recursion (v56), but the super.<self>()
// delegation marker clears recursive_self (mirrors the Conductor onChange* case).
class FancyCalculator extends Calculator {

    @Override
    public void render() {
        super.render();
        render();
    }
}
