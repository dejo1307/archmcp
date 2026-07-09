package rustextractor

import "testing"

func TestComplexity_StraightLine(t *testing.T) {
	ff := extractAST(t, `
fn plain() {
    let x = 1;
    let y = 2;
}
`)
	f, ok := findFact(ff, "pkg.plain")
	if !ok {
		t.Fatal("expected fact for pkg.plain")
	}
	if got, _ := f.Props["cyclomatic"].(int); got != 1 {
		t.Errorf("cyclomatic = %v, want 1", f.Props["cyclomatic"])
	}
}

func TestComplexity_IfElse(t *testing.T) {
	ff := extractAST(t, `
fn check(x: i32) -> i32 {
    if x > 0 {
        1
    } else {
        0
    }
}
`)
	f, _ := findFact(ff, "pkg.check")
	if got, _ := f.Props["cyclomatic"].(int); got != 2 {
		t.Errorf("cyclomatic = %v, want 2 (1 + one if)", f.Props["cyclomatic"])
	}
}

func TestComplexity_MatchArms(t *testing.T) {
	ff := extractAST(t, `
fn describe(x: i32) -> &'static str {
    match x {
        0 => "zero",
        1 => "one",
        _ => "many",
    }
}
`)
	f, _ := findFact(ff, "pkg.describe")
	if got, _ := f.Props["cyclomatic"].(int); got != 4 {
		t.Errorf("cyclomatic = %v, want 4 (1 + three match arms)", f.Props["cyclomatic"])
	}
}

func TestComplexity_Loops(t *testing.T) {
	ff := extractAST(t, `
fn run() {
    while true {
        break;
    }
    for x in 0..10 {
        drop(x);
    }
    loop {
        break;
    }
}
`)
	f, _ := findFact(ff, "pkg.run")
	if got, _ := f.Props["cyclomatic"].(int); got != 4 {
		t.Errorf("cyclomatic = %v, want 4 (1 + while + for + loop)", f.Props["cyclomatic"])
	}
}

func TestComplexity_BooleanOperators(t *testing.T) {
	ff := extractAST(t, `
fn check(a: bool, b: bool, c: bool) -> bool {
    a && b || c
}
`)
	f, _ := findFact(ff, "pkg.check")
	if got, _ := f.Props["cyclomatic"].(int); got != 3 {
		t.Errorf("cyclomatic = %v, want 3 (1 + && + ||)", f.Props["cyclomatic"])
	}
}

func TestComplexity_TryOperator(t *testing.T) {
	ff := extractAST(t, `
fn parse(s: &str) -> Result<i32, ()> {
    let n = s.parse().map_err(|_| ())?;
    Ok(n)
}
`)
	f, _ := findFact(ff, "pkg.parse")
	if got, _ := f.Props["cyclomatic"].(int); got != 2 {
		t.Errorf("cyclomatic = %v, want 2 (1 + one ? operator)", f.Props["cyclomatic"])
	}
}

func TestComplexity_TraitSignatureIsOne(t *testing.T) {
	ff := extractAST(t, `
trait Greeter {
    fn greet(&self) -> String;
}
`)
	f, ok := findFact(ff, "pkg.Greeter.greet")
	if !ok {
		t.Fatal("expected fact for pkg.Greeter.greet")
	}
	if got, _ := f.Props["cyclomatic"].(int); got != 1 {
		t.Errorf("cyclomatic = %v, want 1 (no body to walk)", f.Props["cyclomatic"])
	}
}
