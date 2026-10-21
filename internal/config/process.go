package config

import (
	"os"
	"path/filepath"
)

// SubprocessEnv makes Python tools use the same database and Go binary as their caller.
func (c *Config) SubprocessEnv() ([]string, error) {
	db, err := filepath.Abs(c.DBPath)
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return append(os.Environ(), "JOBWATCH_DB_PATH="+db, "JOBWATCH_BINARY="+binary), nil
}
