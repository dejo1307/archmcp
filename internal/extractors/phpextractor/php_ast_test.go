package phpextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestAST_ClassConstantsAndProperties(t *testing.T) {
	src := `<?php
class Config {
    const VERSION = "1.0";
    public $name;
    protected $secret;
}
`
	byName := symbolsByName(extractFileAST([]byte(src), "src/config.php"))

	c, ok := byName["Config::VERSION"]
	if !ok {
		t.Fatalf("missing class const Config::VERSION; got %v", keys(byName))
	}
	if sk, _ := c.Props["symbol_kind"].(string); sk != facts.SymbolConstant {
		t.Errorf("VERSION symbol_kind = %q, want constant", sk)
	}

	name := byName["Config::$name"]
	if name.Props["exported"] != true {
		t.Errorf("public $name exported = %v, want true", name.Props["exported"])
	}
	secret := byName["Config::$secret"]
	if secret.Props["exported"] != false {
		t.Errorf("protected $secret exported = %v, want false", secret.Props["exported"])
	}
}

func TestAST_SelfMethodCallAndStaticSelf(t *testing.T) {
	src := `<?php
class Worker {
    public function run() {
        $this->process();
        self::log();
    }
    private function process() {}
    private static function log() {}
}
`
	byName := symbolsByName(extractFileAST([]byte(src), "src/worker.php"))
	run := byName["Worker::run"]

	// $this->process() -> bare method name recorded.
	if !hasRel(run, facts.RelCalls, "process") {
		t.Errorf("run should call process; relations %v", run.Relations)
	}
	// self::log() -> self resolved to enclosing class FQN.
	if !hasRel(run, facts.RelCalls, "Worker::log") {
		t.Errorf("run should call Worker::log; relations %v", run.Relations)
	}
}

func TestAST_BracedNamespace(t *testing.T) {
	src := `<?php
namespace App\A {
    class Foo {}
}
namespace App\B {
    class Bar {}
}
`
	byName := symbolsByName(extractFileAST([]byte(src), "src/multi.php"))
	if _, ok := byName["App\\A\\Foo"]; !ok {
		t.Errorf("missing App\\A\\Foo; got %v", keys(byName))
	}
	if _, ok := byName["App\\B\\Bar"]; !ok {
		t.Errorf("missing App\\B\\Bar; got %v", keys(byName))
	}
}

func TestAST_TemplateFileWithHTML(t *testing.T) {
	// WordPress templates interleave HTML and <?php; LanguagePHP must still find the
	// declarations and calls.
	src := `<html><body>
<?php
function render_header() {
    echo get_bloginfo('name');
}
?>
<h1>Title</h1>
<?php render_header(); ?>
</body></html>
`
	byName := symbolsByName(extractFileAST([]byte(src), "themes/acme/header.php"))
	fn, ok := byName["render_header"]
	if !ok {
		t.Fatalf("missing render_header in template file; got %v", keys(byName))
	}
	if !hasRel(fn, facts.RelCalls, "get_bloginfo") {
		t.Errorf("render_header should call get_bloginfo; relations %v", fn.Relations)
	}
}
