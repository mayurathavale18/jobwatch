package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jobwatch/internal/profile"
	"jobwatch/internal/store"
)

func profileFiles(t *testing.T) (configPath, input, db string) {
	t.Helper()
	dir := t.TempDir()
	configPath, input, db = filepath.Join(dir, "config.yaml"), filepath.Join(dir, "profile.json"), filepath.Join(dir, "jobs.db")
	yaml := "db_path: " + filepath.ToSlash(db) + "\ncompanies:\n  - name: Example\n    provider: lever\n    slug: example\n"
	if err := os.WriteFile(configPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../examples/profile.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	return
}

func TestProfileCLIImportExport(t *testing.T) {
	configPath, input, db := profileFiles(t)
	var out bytes.Buffer
	args := []string{"import", "-config", configPath, "-file", input}
	if err := runProfile(args, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("validation-only import created a database")
	}
	if !strings.Contains(out.String(), `"applied":false`) || strings.Contains(out.String(), "Alex") {
		t.Fatalf("validation output should contain status, not candidate data: %s", &out)
	}
	out.Reset()
	args = append(args, "-apply")
	if err := runProfile(args, &out); err != nil {
		t.Fatal(err)
	}
	if err := runProfile(args, &out); !errors.Is(err, store.ErrProfileConflict) {
		t.Fatalf("repeat revision-zero import overwrote profile: %v", err)
	}
	out.Reset()
	if err := runProfile([]string{"export", "-config", configPath}, &out); err != nil {
		t.Fatal(err)
	}
	exported, err := profile.Decode(bytes.NewReader(out.Bytes()))
	if err != nil || exported.Revision != 1 || exported.Profile.Identity.Name != "Alex Example" {
		t.Fatalf("export: %+v %v", exported, err)
	}
	exported.Profile.Identity.Name = "Blair Example"
	// Imported metadata never determines the local write time.
	exported.UpdatedAt = exported.UpdatedAt.AddDate(-10, 0, 0)
	data, _ := json.Marshal(exported)
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runProfile(args, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runProfile([]string{"export", "-config", configPath}, &out); err != nil {
		t.Fatal(err)
	}
	latest, err := profile.Decode(bytes.NewReader(out.Bytes()))
	if err != nil || latest.Revision != 2 || latest.Profile.Identity.Name != "Blair Example" || !latest.UpdatedAt.After(exported.UpdatedAt) {
		t.Fatalf("updated export: %+v %v", latest, err)
	}
}

func TestProfileCLIDatabaseOverrideDoesNotLoadConfig(t *testing.T) {
	_, input, db := profileFiles(t)
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")
	var out bytes.Buffer
	if err := runProfile([]string{"import", "-db", db, "-config", missingConfig, "-file", input, "-apply"}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runProfile([]string{"export", "-db", db, "-config", missingConfig}, &out); err != nil {
		t.Fatal(err)
	}
	snapshot, err := profile.Decode(&out)
	if err != nil || snapshot.Profile.Identity.Name != "Alex Example" {
		t.Fatalf("wrong profile: %+v %v", snapshot, err)
	}
}

func TestProfileCLIRejectsBadInputBeforeOpeningDatabase(t *testing.T) {
	for name, inputJSON := range map[string]string{
		"invalid policy": `{"profile":{"schemaVersion":1,"automation":{"minFitToNotify":101}}}`,
		"unknown fields": `{"profile":{"schemaVersion":1,"credentials":{"token":"example"}}}`,
		"trailing input": `{"profile":{"schemaVersion":1}} {}`,
		"encoded size":   `{"profile":{"schemaVersion":1,"outreach":{"instructions":"` + strings.Repeat("<", profile.MaxImportBytes/4) + `"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath, input, db := profileFiles(t)
			os.WriteFile(input, []byte(inputJSON), 0600)
			var out bytes.Buffer
			if err := runProfile([]string{"import", "-config", configPath, "-file", input, "-apply"}, &out); err == nil {
				t.Fatal("expected rejection")
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatal("invalid import created database")
			}
		})
	}
}

func TestProfileCLIArgumentsAndMissingExport(t *testing.T) {
	configPath, input, db := profileFiles(t)
	for _, args := range [][]string{
		nil, {"delete"}, {"import", "-config", configPath},
		{"import", "-config", configPath, "-file", input, "extra"},
		{"export", "-config", configPath, "-apply"},
		{"export", "-config", configPath},
	} {
		if err := runProfile(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("export or bad arguments created database")
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := runProfile([]string{"export", "-config", configPath}, &bytes.Buffer{}); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("empty database must not synthesize a profile: %v", err)
	}
}
