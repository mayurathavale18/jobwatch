package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"jobwatch/internal/profile"
	"jobwatch/internal/store"
)

func runProfile(args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "import" && args[0] != "export") {
		return fmt.Errorf("usage: jobwatch profile import -file profile.local.json [-apply] [-config config.yaml] | export [-config config.yaml]")
	}
	command := args[0]
	fs := flag.NewFlagSet("profile "+command, flag.ContinueOnError)
	var file *string
	var apply *bool
	if command == "import" {
		file = fs.String("file", "", "profile snapshot JSON file")
		apply = fs.Bool("apply", false, "save after validation; revision must match the stored profile")
	}
	cfg, err := loadConfigFlag(fs, args[1:])
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments after profile %s", command)
	}
	var snapshot profile.Snapshot
	if command == "import" {
		if *file == "" {
			return fmt.Errorf("-file is required")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		snapshot, err = profile.Decode(input)
		input.Close()
		if err != nil {
			return err
		}
		if !*apply {
			return json.NewEncoder(out).Encode(map[string]any{"valid": true, "applied": false, "expectedRevision": snapshot.Revision})
		}
	} else {
		// A mistyped export path must not create an empty database.
		if _, err := os.Stat(cfg.DBPath); err != nil {
			return err
		}
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	if command == "import" {
		snapshot, err = st.SaveProfile(ctx, snapshot.Profile, snapshot.Revision)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{
			"valid": true, "applied": true, "revision": snapshot.Revision, "updatedAt": snapshot.UpdatedAt.Format(time.RFC3339Nano),
		})
	}
	snapshot, err = st.GetProfile(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}
