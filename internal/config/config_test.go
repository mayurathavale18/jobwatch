package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadRejectsWorkdayWithoutHostAndSite(t *testing.T) {
	path := writeConfig(t, `
companies:
  - name: "Wells Fargo"
    provider: workday
    slug: wf
filters: {}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for workday company missing host/site, got nil")
	}
}

func TestLoadAcceptsValidWorkdayCompany(t *testing.T) {
	path := writeConfig(t, `
companies:
  - name: "Wells Fargo"
    provider: workday
    slug: wf
    host: wd1
    site: WellsFargoJobs
filters: {}
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Companies) != 1 {
		t.Fatalf("expected 1 company, got %d", len(cfg.Companies))
	}
	co := cfg.Companies[0]
	if co.Host != "wd1" || co.Site != "WellsFargoJobs" {
		t.Errorf("Host/Site = %q/%q, want wd1/WellsFargoJobs", co.Host, co.Site)
	}
}

func TestLoadRejectsUnsupportedProvider(t *testing.T) {
	path := writeConfig(t, `
companies:
  - name: "Acme"
    provider: bamboohr
    slug: acme
filters: {}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
}
