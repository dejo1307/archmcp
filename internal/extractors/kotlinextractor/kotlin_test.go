package kotlinextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

// --- helpers ---

func extractFromString(t *testing.T, src string, isAndroid bool) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.kt")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return extractFile(f, "pkg/test.kt", isAndroid)
}

func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func findFactsByKind(ff []facts.Fact, kind string) []facts.Fact {
	var result []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			result = append(result, f)
		}
	}
	return result
}

func hasRelation(f facts.Fact, relKind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == relKind && r.Target == target {
			return true
		}
	}
	return false
}

// --- Unit tests for parsing helpers ---

func TestParseSupertypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single", "AppCompatActivity()", []string{"AppCompatActivity"}},
		{"multiple", "ViewModel(), LifecycleObserver", []string{"ViewModel", "LifecycleObserver"}},
		{"generic", "BaseClass<T>(arg)", []string{"BaseClass"}},
		{"empty", "", nil},
		{"with spaces", " Foo , Bar ", []string{"Foo", "Bar"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSupertypes(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("parseSupertypes(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractSupertypesFromText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", " : AppCompatActivity() {", "AppCompatActivity()"},
		{"with constructor params", "(val id: Int) : ViewModel() {", "ViewModel()"},
		{"generic constructor", "<T>(val id: Int) : Base<T>(arg), Serializable {", "Base<T>(arg), Serializable"},
		{"no colon", " {", ""},
		{"param colon skipped", "(name: String) : Repository {", "Repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSupertypesFromText(tt.input)
			if got != tt.want {
				t.Errorf("extractSupertypesFromText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Integration tests via extractFile ---

func TestExtract_DataClass(t *testing.T) {
	ff := extractFromString(t, `
data class User(val name: String, val email: String) {
}
`, false)

	f, ok := findFact(ff, "pkg.User")
	if !ok {
		t.Fatal("expected fact for pkg.User")
	}
	if f.Props["data_class"] != true {
		t.Errorf("data_class = %v, want true", f.Props["data_class"])
	}
	if f.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v, want class", f.Props["symbol_kind"])
	}
}

func TestExtract_SealedClass(t *testing.T) {
	ff := extractFromString(t, `
sealed class Result {
}
`, false)

	f, ok := findFact(ff, "pkg.Result")
	if !ok {
		t.Fatal("expected fact for pkg.Result")
	}
	if f.Props["sealed"] != true {
		t.Errorf("sealed = %v, want true", f.Props["sealed"])
	}
}

func TestExtract_MultiLineConstructor(t *testing.T) {
	ff := extractFromString(t, `
class UserRepository(
    private val api: ApiService,
    private val db: UserDao
) : Repository {
}
`, true)

	f, ok := findFact(ff, "pkg.UserRepository")
	if !ok {
		t.Fatal("expected fact for pkg.UserRepository")
	}
	if !hasRelation(f, facts.RelImplements, "Repository") {
		t.Error("expected implements relation for Repository")
	}
	if f.Props["android_component"] != "repository" {
		t.Errorf("android_component = %v, want repository", f.Props["android_component"])
	}
}

func TestExtract_ComposableFunction(t *testing.T) {
	ff := extractFromString(t, `
@Composable
fun HomeScreen() {
}
`, true)

	f, ok := findFact(ff, "pkg.HomeScreen")
	if !ok {
		t.Fatal("expected fact for pkg.HomeScreen")
	}
	if f.Props["android_component"] != "composable" {
		t.Errorf("android_component = %v, want composable", f.Props["android_component"])
	}
	if f.Props["framework"] != "android" {
		t.Errorf("framework = %v, want android", f.Props["framework"])
	}
}

func TestExtract_HiltViewModel(t *testing.T) {
	ff := extractFromString(t, `
@HiltViewModel
class HomeViewModel @Inject constructor(
    private val repo: UserRepository
) : ViewModel() {
}
`, true)

	f, ok := findFact(ff, "pkg.HomeViewModel")
	if !ok {
		t.Fatal("expected fact for pkg.HomeViewModel")
	}
	if f.Props["android_component"] != "viewmodel" {
		t.Errorf("android_component = %v, want viewmodel", f.Props["android_component"])
	}
	if !hasRelation(f, facts.RelImplements, "ViewModel") {
		t.Error("expected implements relation for ViewModel")
	}
}

func TestExtract_RoomStorage(t *testing.T) {
	tests := []struct {
		annotation  string
		storageKind string
	}{
		{"Entity", "entity"},
		{"Dao", "dao"},
		{"Database", "database"},
	}
	for _, tt := range tests {
		t.Run(tt.annotation, func(t *testing.T) {
			src := "@" + tt.annotation + "\nclass Test" + tt.annotation + " {\n}\n"
			ff := extractFromString(t, src, true)

			storage := findFactsByKind(ff, facts.KindStorage)
			if len(storage) != 1 {
				t.Fatalf("expected 1 storage fact, got %d", len(storage))
			}
			if storage[0].Props["storage_kind"] != tt.storageKind {
				t.Errorf("storage_kind = %v, want %s", storage[0].Props["storage_kind"], tt.storageKind)
			}
			if storage[0].Props["framework"] != "room" {
				t.Errorf("framework = %v, want room", storage[0].Props["framework"])
			}
		})
	}
}

func TestExtract_NoRoomStorage_WithoutAndroid(t *testing.T) {
	ff := extractFromString(t, `
@Entity
class UserEntity {
}
`, false) // isAndroid=false

	storage := findFactsByKind(ff, facts.KindStorage)
	if len(storage) != 0 {
		t.Errorf("expected 0 storage facts when isAndroid=false, got %d", len(storage))
	}
}

func TestExtract_ObjectDeclaration(t *testing.T) {
	ff := extractFromString(t, `
object AppModule : Module {
}
`, false)

	f, ok := findFact(ff, "pkg.AppModule")
	if !ok {
		t.Fatal("expected fact for pkg.AppModule")
	}
	if f.Props["object"] != true {
		t.Errorf("object = %v, want true", f.Props["object"])
	}
	if !hasRelation(f, facts.RelImplements, "Module") {
		t.Error("expected implements relation for Module")
	}
}

func TestExtract_SupertypeExtraction(t *testing.T) {
	ff := extractFromString(t, `
class Foo : Base<T>(), Interface {
}
`, false)

	f, ok := findFact(ff, "pkg.Foo")
	if !ok {
		t.Fatal("expected fact for pkg.Foo")
	}
	if !hasRelation(f, facts.RelImplements, "Base") {
		t.Error("expected implements relation for Base")
	}
	if !hasRelation(f, facts.RelImplements, "Interface") {
		t.Error("expected implements relation for Interface")
	}
}

func TestExtract_SuspendFunction(t *testing.T) {
	ff := extractFromString(t, `
suspend fun fetchUsers() {
}
`, false)

	f, ok := findFact(ff, "pkg.fetchUsers")
	if !ok {
		t.Fatal("expected fact for pkg.fetchUsers")
	}
	if f.Props["suspend"] != true {
		t.Errorf("suspend = %v, want true", f.Props["suspend"])
	}
}

func TestExtract_InterfaceDeclaration(t *testing.T) {
	ff := extractFromString(t, `
interface UserRepository {
    suspend fun getUsers(): List<User>
}
`, false)

	f, ok := findFact(ff, "pkg.UserRepository")
	if !ok {
		t.Fatal("expected fact for pkg.UserRepository")
	}
	if f.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("symbol_kind = %v, want interface", f.Props["symbol_kind"])
	}
}

func TestExtract_Imports(t *testing.T) {
	ff := extractFromString(t, `
import kotlinx.coroutines.flow.Flow
import android.os.Bundle
`, false)

	deps := findFactsByKind(ff, facts.KindDependency)
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependency facts, got %d", len(deps))
	}
}

func TestExtract_EnumClass(t *testing.T) {
	ff := extractFromString(t, `
enum class Direction {
    NORTH, SOUTH, EAST, WEST
}
`, false)

	f, ok := findFact(ff, "pkg.Direction")
	if !ok {
		t.Fatal("expected fact for pkg.Direction")
	}
	if f.Props["enum"] != true {
		t.Errorf("enum = %v, want true", f.Props["enum"])
	}
}
