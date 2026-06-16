package javaextractor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		setup map[string]string
		want  bool
	}{
		{"maven pom", map[string]string{"pom.xml": "<project/>"}, true},
		{"gradle java", map[string]string{"build.gradle": "plugins {}", "src/main/java/A.java": "class A {}"}, true},
		{"bare java source", map[string]string{"src/main/java/A.java": "class A {}"}, true},
		{"gradle kotlin only", map[string]string{"build.gradle.kts": "plugins {}", "app/src/main/java/A.kt": "class A"}, false},
		{"no java", map[string]string{"main.go": "package main"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tt.setup {
				abs := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := New().Detect(dir)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got != tt.want {
				t.Errorf("Detect = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "java" {
		t.Errorf("Name = %q, want java", got)
	}
}
