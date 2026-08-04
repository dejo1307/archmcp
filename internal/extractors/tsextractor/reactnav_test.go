package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func extractReactNav(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	pkg := `{"dependencies": {"@react-navigation/native": "^6.0.0", "react": "^18.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	var rel []string
	for f, c := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, f)
	}
	ff, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return ff
}

func TestReactNav_ScreensBecomeRoutesWithHandlers(t *testing.T) {
	ff := extractReactNav(t, map[string]string{
		"src/screens/home.js": `export default function HomeScreen() { return null; }
`,
		"src/navigation/root.js": `import HomeScreen from '../screens/home';
const Stack = createNativeStackNavigator();
export function Root() {
  return (
    <Stack.Navigator>
      <Stack.Screen name="Home" component={HomeScreen} />
      <Stack.Screen name="Profile" component={ProfileScreen} />
    </Stack.Navigator>
  );
}
`,
	})
	home := findEmberFact(ff, facts.KindRoute, "Home")
	if home == nil {
		t.Fatal("Home screen route missing")
	}
	if home.Props["framework"] != "react-navigation" || home.Props["type"] != "page" {
		t.Errorf("props = %v", home.Props)
	}
	if !home.HasRelation(facts.RelHandledBy, "src/screens.HomeScreen") {
		t.Errorf("relations = %v, want handled_by the imported component", home.Relations)
	}
	profile := findEmberFact(ff, facts.KindRoute, "Profile")
	if profile == nil {
		t.Fatal("Profile route missing")
	}
	for _, r := range profile.Relations {
		if r.Kind == facts.RelHandledBy {
			t.Errorf("Profile bound %v — its component is not an import this file states", r)
		}
	}
}

func TestReactNav_NavigateLiteralsAttachToEnclosingSymbol(t *testing.T) {
	ff := extractReactNav(t, map[string]string{
		"src/components/banner.js": `export function Banner({ navigation }) {
  const open = () => navigation.navigate('Profile', { id: 1 });
  const back = () => navigation.push('Home');
  const dyn = () => navigation.navigate(target);
  return null;
}
`,
	})
	banner := findEmberFact(ff, facts.KindSymbol, "src/components.Banner")
	if banner == nil {
		t.Fatal("Banner symbol missing")
	}
	got, _ := banner.Props[NavRouteLinksProp].([]string)
	if !reflect.DeepEqual(got, []string{"Home", "Profile"}) {
		t.Errorf("%s = %v, want literal targets only", NavRouteLinksProp, got)
	}
}
