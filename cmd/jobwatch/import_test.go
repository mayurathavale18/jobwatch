package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportPreviewDoesNotCreateDatabase(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	db := filepath.Join(dir, "absent.db")
	input := filepath.Join(dir, "alerts.json")
	if err := os.WriteFile(config, []byte("db_path: "+filepath.ToSlash(db)+"\ncompanies:\n  - name: Acme\n    provider: lever\n    slug: acme\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(`[{"url":"https://jobs.lever.co/acme/123","company":"Acme","title":"Backend Engineer","source":"Exported alert"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runImportJobs([]string{"-config", config, "-file", input}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("preview created database: %v", err)
	}
}
