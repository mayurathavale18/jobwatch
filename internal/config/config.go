// Package config loads and validates jobwatch's single YAML config file.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Company struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	// Slug identifies the company's own board for per-company providers
	// (greenhouse/lever/ashby/workday). For search-aggregator providers
	// (remoteok/wwr), which return jobs across many real companies in one
	// Fetch call, it's just a label -- each returned Job carries its own
	// real CompanySlug/CompanyName instead.
	Slug string `yaml:"slug"`
	// Host and Site are Workday-only: Workday postings live at
	// https://{slug}.{host}.myworkdayjobs.com/wday/cxs/{slug}/{site}/jobs,
	// where slug is the tenant, host is the numbered subdomain (e.g. "wd1",
	// "wd3"), and site is the career-site path segment (e.g.
	// "WellsFargoJobs"). Both are required when provider is "workday".
	Host string `yaml:"host,omitempty"`
	Site string `yaml:"site,omitempty"`
}

type Filters struct {
	IncludeKeywords  []string `yaml:"include_keywords"`
	ExcludeKeywords  []string `yaml:"exclude_keywords"`
	LocationsInclude []string `yaml:"locations_include"`
}

type Telegram struct {
	BotTokenEnv string `yaml:"bot_token_env"`
	ChatIDEnv   string `yaml:"chat_id_env"`
}

type Dashboard struct {
	Addr string `yaml:"addr"`
}

type Config struct {
	Companies []Company `yaml:"companies"`
	Filters   Filters   `yaml:"filters"`
	Telegram  Telegram  `yaml:"telegram"`
	Dashboard Dashboard `yaml:"dashboard"`
	DBPath    string    `yaml:"db_path"`
}

// Load reads and validates the YAML config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Companies) == 0 {
		return fmt.Errorf("companies: at least one company required")
	}
	validProviders := map[string]bool{
		"greenhouse": true, "lever": true, "ashby": true, "workday": true,
		"remoteok": true, "wwr": true,
	}
	for i, co := range c.Companies {
		if co.Name == "" {
			return fmt.Errorf("companies[%d]: name required", i)
		}
		if co.Slug == "" {
			return fmt.Errorf("companies[%d] (%s): slug required", i, co.Name)
		}
		provider := strings.ToLower(co.Provider)
		if !validProviders[provider] {
			return fmt.Errorf("companies[%d] (%s): unsupported provider %q (want greenhouse, lever, ashby, workday, remoteok, or wwr)", i, co.Name, co.Provider)
		}
		if provider == "workday" && (co.Host == "" || co.Site == "") {
			return fmt.Errorf("companies[%d] (%s): workday requires host and site", i, co.Name)
		}
	}
	if c.DBPath == "" {
		c.DBPath = "./jobwatch.db"
	}
	if c.Dashboard.Addr == "" {
		c.Dashboard.Addr = "127.0.0.1:8787"
	}
	if c.Telegram.BotTokenEnv == "" {
		c.Telegram.BotTokenEnv = "JOBWATCH_TG_TOKEN"
	}
	if c.Telegram.ChatIDEnv == "" {
		c.Telegram.ChatIDEnv = "JOBWATCH_TG_CHAT"
	}
	return nil
}

// BotToken reads the Telegram bot token from the configured env var.
func (c *Config) BotToken() string {
	return os.Getenv(c.Telegram.BotTokenEnv)
}

// ChatID reads the Telegram chat ID from the configured env var.
func (c *Config) ChatID() string {
	return os.Getenv(c.Telegram.ChatIDEnv)
}
