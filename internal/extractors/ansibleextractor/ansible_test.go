package ansibleextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func extractAnsible(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
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
	ok, err := New().Detect(dir)
	if err != nil || !ok {
		t.Fatalf("Detect = %v, %v", ok, err)
	}
	ff, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return ff
}

func find(ff []facts.Fact, kind, name string) *facts.Fact {
	for i := range ff {
		if ff[i].Kind == kind && ff[i].Name == name {
			return &ff[i]
		}
	}
	return nil
}

func TestAnsible_PlaysRolesAndTaskRefs(t *testing.T) {
	ff := extractAnsible(t, map[string]string{
		"site.yml": `---
- name: Provision web tier
  hosts: web
  roles:
    - nginx
    - role: app
      vars:
        port: 3000
    - unknown_role
`,
		"roles/nginx/tasks/main.yml": `---
- name: install
  package:
    name: nginx
`,
		"roles/nginx/templates/site.conf.j2": "server {}\n",
		"roles/app/tasks/main.yml": `---
- name: base config
  import_role:
    name: nginx
- name: dynamic include stays out
  include_role:
    name: "{{ chosen_role }}"
`,
	})

	playFact := find(ff, facts.KindSymbol, "Provision web tier")
	if playFact == nil {
		t.Fatal("play missing")
	}
	if !playFact.HasRelation(facts.RelDependsOn, "roles/nginx.nginx") ||
		!playFact.HasRelation(facts.RelDependsOn, "roles/app.app") {
		t.Errorf("play relations = %v, want both string and hash role forms", playFact.Relations)
	}
	for _, r := range playFact.Relations {
		if strings := r.Target; r.Kind == facts.RelDependsOn && strings == "unknown_role" {
			t.Error("undeclared role drew an edge")
		}
	}
	app := find(ff, facts.KindSymbol, "roles/app.app")
	if app == nil || !app.HasRelation(facts.RelDependsOn, "roles/nginx.nginx") {
		t.Errorf("app role = %+v, want import_role edge to nginx (and none for the templated include)", app)
	}
	nginx := find(ff, facts.KindSymbol, "roles/nginx.nginx")
	if nginx == nil || nginx.Props["template_count"] != 1 {
		t.Errorf("nginx role = %+v, want its .j2 counted, never parsed", nginx)
	}
}

func TestAnsible_DetectDemandsMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := New().Detect(dir)
	if err != nil || ok {
		t.Fatalf("Detect = %v, %v — arbitrary YAML must not read as Ansible", ok, err)
	}
}
