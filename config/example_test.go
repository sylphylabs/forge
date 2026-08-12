package config_test

// The examples in this file mirror the config snippets in
// docs/agent/application.md so that the guide cannot drift from the API
// without breaking the build. When one of these stops compiling, fix the
// guide together with the example.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sylphylabs/forge/config"
	"github.com/sylphylabs/forge/config/file"
)

// Example mirrors the guide's Config section: New loads every source on
// construction, Scan populates a struct, and config.Get is the generic
// shorthand for one key.
func Example() {
	dir, err := os.MkdirTemp("", "forge-config")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	cfg := []byte(`{"server": {"http": {"port": 8000, "timeout": "1s"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); err != nil {
		fmt.Println(err)
		return
	}

	c, err := config.New(context.Background(), config.WithSource(file.NewSource(dir)))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer c.Close()

	var bc struct {
		Server struct {
			HTTP struct {
				Port int64 `json:"port"`
			} `json:"http"`
		} `json:"server"`
	}
	if err := c.Scan(&bc); err != nil {
		fmt.Println(err)
		return
	}

	port, err := config.Get[int](c, "server.http.port")
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(bc.Server.HTTP.Port, port)
	// Output: 8000 8000
}

// Example_watch mirrors the guide's dynamic reconfiguration snippet: watch a
// key and the observer runs on every source change.
func Example_watch() {
	dir, err := os.MkdirTemp("", "forge-config")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	cfg := []byte(`{"server": {"http": {"timeout": "1s"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); err != nil {
		fmt.Println(err)
		return
	}

	c, err := config.New(context.Background(), config.WithSource(file.NewSource(dir)))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer c.Close()

	err = c.Watch("server.http.timeout", func(_ string, _ config.Value) {
		// applied on every source change
	})
	fmt.Println(err)
	// Output: <nil>
}
