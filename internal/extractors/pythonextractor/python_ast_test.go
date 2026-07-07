package pythonextractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// astExtract is a helper that writes src to a temp file and runs extractFileAST.
func astExtract(t *testing.T, filename, src string, isDjango bool) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return extractFileAST([]byte(src), filename, isDjango, nil)
}

// astExtractWithIndex runs the index pass over all provided sources, then
// extracts facts from targetFile using that index.
func astExtractWithIndex(t *testing.T, files map[string]string, targetFile string, isDjango bool) []facts.Fact {
	t.Helper()
	idx := &pySymbolIndex{classes: make(map[string]*pyClassInfo)}
	for filename, src := range files {
		buildFileIndex([]byte(src), filename, idx)
	}
	finalizeImplMap(idx)
	src, ok := files[targetFile]
	if !ok {
		t.Fatalf("targetFile %q not in files map", targetFile)
	}
	return extractFileAST([]byte(src), targetFile, isDjango, idx)
}

// relsByKind returns all relations of a given kind from a fact.
func relsByKind(f facts.Fact, kind string) []string {
	var out []string
	for _, r := range f.Relations {
		if r.Kind == kind {
			out = append(out, r.Target)
		}
	}
	return out
}

// --- Call graph tests ---

func TestAST_SameModuleFunctionCall(t *testing.T) {
	src := `
def helper():
    pass

def main():
    helper()
`
	result := astExtract(t, "svc.py", src, false)
	idx := byName(result)

	mainFact, ok := idx["svc.main"]
	if !ok {
		t.Fatalf("missing svc.main; keys: %v", keys(idx))
	}
	calls := relsByKind(mainFact, facts.RelCalls)
	if len(calls) == 0 {
		t.Fatal("svc.main: expected RelCalls to svc.helper, got none")
	}
	found := false
	for _, c := range calls {
		if c == "svc.helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("svc.main: RelCalls = %v, want svc.helper", calls)
	}
}

func TestAST_SelfMethodCall(t *testing.T) {
	src := `
class Service:
    def _do_work(self):
        pass

    def run(self):
        self._do_work()
`
	result := astExtract(t, "svc.py", src, false)
	idx := byName(result)

	runFact, ok := idx["svc.Service.run"]
	if !ok {
		t.Fatalf("missing svc.Service.run; keys: %v", keys(idx))
	}
	calls := relsByKind(runFact, facts.RelCalls)
	found := false
	for _, c := range calls {
		if c == "svc.Service._do_work" {
			found = true
		}
	}
	if !found {
		t.Errorf("Service.run: RelCalls = %v, want svc.Service._do_work", calls)
	}
}

func TestAST_Constructor_RelInstantiates(t *testing.T) {
	src := `
class Order:
    pass

def create():
    o = Order()
    return o
`
	result := astExtract(t, "models.py", src, false)
	idx := byName(result)

	createFact, ok := idx["models.create"]
	if !ok {
		t.Fatalf("missing models.create; keys: %v", keys(idx))
	}
	insts := relsByKind(createFact, facts.RelInstantiates)
	found := false
	for _, i := range insts {
		if i == "Order" {
			found = true
		}
	}
	if !found {
		t.Errorf("create: RelInstantiates = %v, want Order", insts)
	}
}

func TestAST_NoEdgeForBuiltins(t *testing.T) {
	src := `
def process(items):
    result = list(map(str, items))
    print(len(result))
    return sorted(result)
`
	result := astExtract(t, "util.py", src, false)
	idx := byName(result)

	fn, ok := idx["util.process"]
	if !ok {
		t.Fatalf("missing util.process")
	}
	calls := relsByKind(fn, facts.RelCalls)
	for _, c := range calls {
		if c == "util.list" || c == "util.print" || c == "util.sorted" || c == "util.map" || c == "util.str" || c == "util.len" {
			t.Errorf("process: should not emit call edge to builtin, got %q", c)
		}
	}
}

func TestAST_ReturnType_FromAST(t *testing.T) {
	// tree-sitter reads the return type node directly — no regex needed.
	src := `
def get_user(user_id: int) -> Optional[str]:
    pass

def create_order(
    items: list,
    total: float,
) -> dict[str, Any]:
    pass
`
	result := astExtract(t, "api.py", src, false)
	idx := byName(result)

	cases := []struct{ name, want string }{
		{"api.get_user", "Optional[str]"},
		{"api.create_order", "dict[str, Any]"},
	}
	for _, tc := range cases {
		fn, ok := idx[tc.name]
		if !ok {
			t.Fatalf("missing %q; keys: %v", tc.name, keys(idx))
		}
		if fn.Props["return_type"] != tc.want {
			t.Errorf("%s: return_type = %v, want %q", tc.name, fn.Props["return_type"], tc.want)
		}
	}
}

func TestAST_NestedClass(t *testing.T) {
	src := `
class Outer:
    class Inner:
        def method(self):
            pass
`
	result := astExtract(t, "nested.py", src, false)
	idx := byName(result)

	if _, ok := idx["nested.Outer"]; !ok {
		t.Errorf("missing nested.Outer; keys: %v", keys(idx))
	}
	if _, ok := idx["nested.Outer.Inner"]; !ok {
		t.Errorf("missing nested.Outer.Inner; keys: %v", keys(idx))
	}
	if _, ok := idx["nested.Outer.Inner.method"]; !ok {
		t.Errorf("missing nested.Outer.Inner.method; keys: %v", keys(idx))
	}
}

func TestAST_AsyncFunction(t *testing.T) {
	src := `
async def fetch_data(url: str) -> bytes:
    pass
`
	result := astExtract(t, "client.py", src, false)
	idx := byName(result)

	fn, ok := idx["client.fetch_data"]
	if !ok {
		t.Fatalf("missing client.fetch_data")
	}
	if fn.Props["async"] != true {
		t.Errorf("fetch_data: async = %v, want true", fn.Props["async"])
	}
	if fn.Props["return_type"] != "bytes" {
		t.Errorf("fetch_data: return_type = %v, want bytes", fn.Props["return_type"])
	}
}

func TestAST_DecoratorProps(t *testing.T) {
	src := `
class Repo:
    @staticmethod
    def from_dict(d):
        pass

    @classmethod
    def create(cls):
        pass

    @property
    def name(self):
        return self._name
`
	result := astExtract(t, "repo.py", src, false)
	idx := byName(result)

	cases := []struct {
		name string
		prop string
		want any
	}{
		{"repo.Repo.from_dict", "static", true},
		{"repo.Repo.create", "class_method", true},
		{"repo.Repo.name", "property", true},
	}
	for _, tc := range cases {
		fn, ok := idx[tc.name]
		if !ok {
			t.Fatalf("missing %q; keys: %v", tc.name, keys(idx))
		}
		if fn.Props[tc.prop] != tc.want {
			t.Errorf("%s: %s = %v, want %v", tc.name, tc.prop, fn.Props[tc.prop], tc.want)
		}
	}
}

// TestAST_InitReexports verifies that from-imports in __init__.py record the
// imported short names in the dependency fact's "reexports" prop, and that
// non-__init__ files do not.
func TestAST_InitReexports(t *testing.T) {
	src := "from .sub import PublicThing, OtherThing\n"

	initFacts := astExtract(t, "pkg/__init__.py", src, false)
	var dep *facts.Fact
	for i := range initFacts {
		if initFacts[i].Kind == facts.KindDependency {
			dep = &initFacts[i]
			break
		}
	}
	if dep == nil {
		t.Fatal("no dependency fact emitted for from-import")
	}
	names, ok := dep.Props["reexports"].([]string)
	if !ok {
		t.Fatalf("reexports prop missing or wrong type: %#v", dep.Props["reexports"])
	}
	want := map[string]bool{"PublicThing": true, "OtherThing": true}
	if len(names) != 2 || !want[names[0]] || !want[names[1]] {
		t.Errorf("reexports = %v, want PublicThing & OtherThing", names)
	}

	// A non-__init__ file must NOT record reexports.
	modFacts := astExtract(t, "pkg/mod.py", src, false)
	for _, f := range modFacts {
		if f.Kind == facts.KindDependency {
			if _, ok := f.Props["reexports"]; ok {
				t.Errorf("non-__init__ file should not record reexports, got %v", f.Props["reexports"])
			}
		}
	}
}

func TestAST_AbstractClassDetection(t *testing.T) {
	src := `
from abc import ABC, ABCMeta, abstractmethod
from typing import Protocol

class FromABC(ABC):
    pass

class FromProtocol(Protocol):
    def run(self): ...

class FromMeta(metaclass=ABCMeta):
    pass

class HasAbstractMethod:
    @abstractmethod
    def do(self):
        ...

class Concrete:
    def do(self):
        return 1
`
	idx := byName(astExtract(t, "svc.py", src, false))

	for _, name := range []string{"svc.FromABC", "svc.FromProtocol", "svc.FromMeta", "svc.HasAbstractMethod"} {
		fn, ok := idx[name]
		if !ok {
			t.Fatalf("missing %q; keys: %v", name, keys(idx))
		}
		if fn.Props["abstract"] != true {
			t.Errorf("%s: abstract = %v, want true", name, fn.Props["abstract"])
		}
	}
	if c := idx["svc.Concrete"]; c.Props["abstract"] == true {
		t.Error("svc.Concrete should not be abstract")
	}
}

// TestAST_ExportedProp verifies the leading-underscore export convention.
func TestAST_ExportedProp(t *testing.T) {
	src := `
class PublicClass:
    pass

class _PrivateClass:
    pass

def public_fn():
    pass

def _private_fn():
    pass
`
	idx := byName(astExtract(t, "svc.py", src, false))
	cases := map[string]bool{
		"svc.PublicClass":   true,
		"svc._PrivateClass": false,
		"svc.public_fn":     true,
		"svc._private_fn":   false,
	}
	for name, want := range cases {
		fn, ok := idx[name]
		if !ok {
			t.Fatalf("missing %q; keys: %v", name, keys(idx))
		}
		if fn.Props["exported"] != want {
			t.Errorf("%s: exported = %v, want %v", name, fn.Props["exported"], want)
		}
	}
}

func TestAST_SQLAlchemyTable(t *testing.T) {
	src := `
from sqlalchemy import Column, Integer, String
from sqlalchemy.orm import DeclarativeBase

class Base(DeclarativeBase):
    pass

class Product(Base):
    __tablename__ = "products"
    id = Column(Integer, primary_key=True)
    name = Column(String)
`
	result := astExtract(t, "models.py", src, false)
	storages := factsByKind(result, facts.KindStorage)
	if len(storages) != 1 {
		t.Fatalf("expected 1 storage fact, got %d: %v", len(storages), storages)
	}
	if storages[0].Name != "products" {
		t.Errorf("storage name = %q, want products", storages[0].Name)
	}
	if storages[0].Props["framework"] != "sqlalchemy" {
		t.Errorf("storage framework = %v, want sqlalchemy", storages[0].Props["framework"])
	}
}

func TestAST_ImportEdges(t *testing.T) {
	src := `
import os
from pathlib import Path
from . import utils
`
	result := astExtract(t, "mymod.py", src, false)
	deps := factsByKind(result, facts.KindDependency)
	if len(deps) < 3 {
		t.Errorf("expected >= 3 dependency facts, got %d", len(deps))
	}
	// Each dep must carry a RelImports relation.
	for _, d := range deps {
		found := false
		for _, r := range d.Relations {
			if r.Kind == facts.RelImports {
				found = true
			}
		}
		if !found {
			t.Errorf("dependency %q missing RelImports relation", d.Name)
		}
	}
}

// --- Symbol resolution tests ---

func TestAST_TypedParam_AttributeCall(t *testing.T) {
	src := `
from .queue import Queue

def consume(q: Queue):
    q.pop()
`
	result := astExtract(t, "app/worker.py", src, false)
	idx := byName(result)

	fn, ok := idx["app/worker.consume"]
	if !ok {
		t.Fatalf("missing app/worker.consume; keys: %v", keys(idx))
	}
	calls := relsByKind(fn, facts.RelCalls)
	found := false
	for _, c := range calls {
		if c == "app/queue.Queue.pop" {
			found = true
		}
	}
	if !found {
		t.Errorf("consume: RelCalls = %v, want app/queue.Queue.pop", calls)
	}
}

func TestAST_ConstructorAssignment_AttributeCall(t *testing.T) {
	src := `
from .repo import UserRepo

def handle():
    repo = UserRepo()
    repo.find(1)
`
	result := astExtract(t, "app/handler.py", src, false)
	idx := byName(result)

	fn, ok := idx["app/handler.handle"]
	if !ok {
		t.Fatalf("missing app/handler.handle; keys: %v", keys(idx))
	}
	calls := relsByKind(fn, facts.RelCalls)
	found := false
	for _, c := range calls {
		if c == "app/repo.UserRepo.find" {
			found = true
		}
	}
	if !found {
		t.Errorf("handle: RelCalls = %v, want app/repo.UserRepo.find", calls)
	}
}

func TestAST_AnnotatedAssignment_AttributeCall(t *testing.T) {
	src := `
from .logger import FileLogger

def process():
    log: FileLogger = FileLogger()
    log.write("done")
`
	result := astExtract(t, "app/svc.py", src, false)
	idx := byName(result)

	fn, ok := idx["app/svc.process"]
	if !ok {
		t.Fatalf("missing app/svc.process; keys: %v", keys(idx))
	}
	calls := relsByKind(fn, facts.RelCalls)
	found := false
	for _, c := range calls {
		if c == "app/logger.FileLogger.write" {
			found = true
		}
	}
	if !found {
		t.Errorf("process: RelCalls = %v, want app/logger.FileLogger.write", calls)
	}
}

func TestAST_NoPhantomEdge_UnresolvableReceiver(t *testing.T) {
	src := `
def handle(req):
    req.method()
`
	result := astExtract(t, "app/h.py", src, false)
	idx := byName(result)

	fn, ok := idx["app/h.handle"]
	if !ok {
		t.Fatalf("missing app/h.handle")
	}
	calls := relsByKind(fn, facts.RelCalls)
	for _, c := range calls {
		if strings.Contains(c, ".method") {
			t.Errorf("handle: unexpected call edge to unresolvable receiver: %q", c)
		}
	}
}

func TestAST_AbstractClass_ConcreteImplementorCalls(t *testing.T) {
	files := map[string]string{
		"app/interfaces.py": `
from abc import ABC, abstractmethod

class Logger(ABC):
    @abstractmethod
    def write(self, msg):
        pass
`,
		"app/loggers.py": `
from .interfaces import Logger

class FileLogger(Logger):
    def write(self, msg):
        pass
`,
		"app/service.py": `
from .interfaces import Logger

class OrderService:
    def process(self, logger: Logger):
        logger.write("order processed")
`,
	}

	result := astExtractWithIndex(t, files, "app/service.py", false)
	idx := byName(result)

	fn, ok := idx["app/service.OrderService.process"]
	if !ok {
		t.Fatalf("missing app/service.OrderService.process; keys: %v", keys(idx))
	}
	calls := relsByKind(fn, facts.RelCalls)

	wantDirect := "app/interfaces.Logger.write"
	wantConcrete := "app/loggers.FileLogger.write"
	foundDirect, foundConcrete := false, false
	for _, c := range calls {
		if c == wantDirect {
			foundDirect = true
		}
		if c == wantConcrete {
			foundConcrete = true
		}
	}
	if !foundDirect {
		t.Errorf("process: missing RelCalls to abstract %q; got %v", wantDirect, calls)
	}
	if !foundConcrete {
		t.Errorf("process: missing RelCalls to concrete %q; got %v", wantConcrete, calls)
	}
}

func TestAST_IsAbstract_Detection(t *testing.T) {
	src := `
from abc import ABC, abstractmethod

class MyInterface(ABC):
    @abstractmethod
    def execute(self):
        pass

class ConcreteImpl(MyInterface):
    def execute(self):
        pass
`
	idx := &pySymbolIndex{classes: make(map[string]*pyClassInfo)}
	buildFileIndex([]byte(src), "app/types.py", idx)

	iface, ok := idx.classes["app/types.MyInterface"]
	if !ok {
		t.Fatal("missing app/types.MyInterface in index")
	}
	if !iface.isAbstract {
		t.Error("MyInterface: expected isAbstract=true")
	}

	impl, ok := idx.classes["app/types.ConcreteImpl"]
	if !ok {
		t.Fatal("missing app/types.ConcreteImpl in index")
	}
	if impl.isAbstract {
		t.Error("ConcreteImpl: expected isAbstract=false")
	}
}

// firstOfKind returns the first fact of the given kind, or a zero fact.
func firstOfKind(ff []facts.Fact, kind string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Kind == kind {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// --- Absolute-import call-edge emission (pre-resolution dotted targets) ---

func TestAST_AbsoluteImportEmitsCallEdge(t *testing.T) {
	src := `
from airflow.api.common.airflow_health import get_airflow_health

def get_health():
    return get_airflow_health()
`
	result := astExtract(t, "monitor.py", src, false)
	idx := byName(result)
	fn, ok := idx["monitor.get_health"]
	if !ok {
		t.Fatalf("missing monitor.get_health; keys: %v", keys(idx))
	}
	calls := relsByKind(fn, facts.RelCalls)
	want := "airflow.api.common.airflow_health.get_airflow_health"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("get_health: RelCalls = %v, want a call to %q (absolute import must emit an edge)", calls, want)
	}
}

func TestAST_TopLevelCallEmitsFileRef(t *testing.T) {
	src := `
from airflow.api_fastapi.app import cached_app

app = cached_app(apps="all")
`
	result := astExtract(t, "main.py", src, false)
	fr, ok := firstOfKind(result, facts.KindFileRef)
	if !ok {
		t.Fatal("expected a KindFileRef fact for the module-level cached_app() call, got none")
	}
	calls := relsByKind(fr, facts.RelCalls)
	want := "airflow.api_fastapi.app.cached_app"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("file_ref: RelCalls = %v, want %q", calls, want)
	}
}

func TestAST_DecoratorEmitsRef(t *testing.T) {
	src := `
from airflow.utils.session import provide_session

@provide_session
def do_work(session=None):
    pass
`
	result := astExtract(t, "svc.py", src, false)
	fr, ok := firstOfKind(result, facts.KindFileRef)
	if !ok {
		t.Fatal("expected a KindFileRef fact for the @provide_session decorator, got none")
	}
	calls := relsByKind(fr, facts.RelCalls)
	want := "airflow.utils.session.provide_session"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("file_ref: RelCalls = %v, want a decorator use of %q", calls, want)
	}
}

// --- Pass 2: lazy imports, value-refs, param defaults, decorator args ---

func TestAST_LazyImportInsideFunctionResolves(t *testing.T) {
	src := `
def load():
    from airflow import plugins_manager
    return plugins_manager.get_priority_weight_strategy_plugins()
`
	result := astExtract(t, "svc.py", src, false)
	fn := byName(result)["svc.load"]
	calls := relsByKind(fn, facts.RelCalls)
	want := "airflow.plugins_manager.get_priority_weight_strategy_plugins"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("load: RelCalls = %v, want a call to %q via the function-local import", calls, want)
	}
}

func TestAST_CallArgumentValueRefEmitsEdge(t *testing.T) {
	src := `
from airflow.auth.utils import parse_login_body

def register():
    return Depends(parse_login_body)
`
	result := astExtract(t, "svc.py", src, false)
	fn := byName(result)["svc.register"]
	calls := relsByKind(fn, facts.RelCalls)
	want := "airflow.auth.utils.parse_login_body"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("register: RelCalls = %v, want a value-ref edge to %q", calls, want)
	}
}

func TestAST_ParameterDefaultCallEmitsEdge(t *testing.T) {
	src := `
from airflow.auth.utils import parse_login_body

def handler(body = Depends(parse_login_body)):
    pass
`
	result := astExtract(t, "svc.py", src, false)
	fn := byName(result)["svc.handler"]
	calls := relsByKind(fn, facts.RelCalls)
	want := "airflow.auth.utils.parse_login_body"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("handler: RelCalls (incl. param defaults) = %v, want %q", calls, want)
	}
}

func TestAST_DecoratorArgumentCallEmitsFileRef(t *testing.T) {
	src := `
from airflow.security import requires_access_asset

@router.get("/x", dependencies=[Depends(requires_access_asset(method="GET"))])
def endpoint():
    pass
`
	result := astExtract(t, "routes.py", src, false)
	fr, ok := firstOfKind(result, facts.KindFileRef)
	if !ok {
		t.Fatal("expected a KindFileRef fact for the decorator-argument call, got none")
	}
	calls := relsByKind(fr, facts.RelCalls)
	want := "airflow.security.requires_access_asset"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("file_ref: RelCalls = %v, want the decorator-arg factory call %q", calls, want)
	}
}

func TestAST_ClickCommandTagged(t *testing.T) {
	src := `
import click

@click.command()
def build():
    pass

@click.group()
def cli():
    pass

def plain():
    pass
`
	result := astExtract(t, "commands.py", src, false)
	idx := byName(result)
	for _, name := range []string{"commands.build", "commands.cli"} {
		fn, ok := idx[name]
		if !ok {
			t.Fatalf("missing %q; keys: %v", name, keys(idx))
		}
		if fn.Props["cli_command"] != true {
			t.Errorf("%s: cli_command = %v, want true", name, fn.Props["cli_command"])
		}
	}
	if idx["commands.plain"].Props["cli_command"] == true {
		t.Error("commands.plain: cli_command should be unset for a non-click function")
	}
}
