package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSubprocessEnvUsesSelectedDatabaseAndRunningBinary(t *testing.T) {
	t.Setenv("JOBWATCH_DB_PATH", "wrong-database")
	t.Setenv("JOBWATCH_BINARY", "wrong-binary")
	cfg := Config{DBPath: filepath.Join("data", "selected.db")}
	env, err := cfg.SubprocessEnv()
	if err != nil {
		t.Fatal(err)
	}
	// os/exec resolves duplicate environment keys in favor of the last value.
	cmd := exec.Command("unused")
	cmd.Env = env
	values := make(map[string]string)
	for _, item := range cmd.Environ() {
		for i := range item {
			if item[i] == '=' {
				values[item[:i]] = item[i+1:]
				break
			}
		}
	}
	wantDB, _ := filepath.Abs(cfg.DBPath)
	wantBinary, _ := os.Executable()
	if values["JOBWATCH_DB_PATH"] != wantDB || values["JOBWATCH_BINARY"] != wantBinary {
		t.Fatalf("selected process context was not forwarded: database=%q binary=%q", values["JOBWATCH_DB_PATH"], values["JOBWATCH_BINARY"])
	}
}
