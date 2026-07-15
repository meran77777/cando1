package config

import (
	"path/filepath"
	"testing"
)

// TestExampleConfigsParse ensures every shipped example config loads and
// validates against the current schema.
func TestExampleConfigsParse(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no example configs found")
	}
	for _, m := range matches {
		m := m
		t.Run(filepath.Base(m), func(t *testing.T) {
			if _, err := Load(m); err != nil {
				t.Fatalf("%s failed to load: %v", m, err)
			}
		})
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name string
		c    Config
	}{
		{"no role", Config{}},
		{"both roles", Config{Server: &ServerConfig{BindAddr: "x", Transport: "tls", Token: "t"}, Client: &ClientConfig{ServerAddr: "y", Transport: "tls", Token: "t", Services: []ClientTarget{{Name: "a", LocalAddr: "z"}}}}},
		{"server no token", Config{Server: &ServerConfig{BindAddr: "x", Transport: "tls"}}},
		{"bad transport", Config{Server: &ServerConfig{BindAddr: "x", Transport: "smoke", Token: "t"}}},
		{"client no services", Config{Client: &ClientConfig{ServerAddr: "y", Transport: "tls", Token: "t"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.normalize()
			if err := c.Validate(); err == nil {
				t.Fatalf("expected validation error for %q", tc.name)
			}
		})
	}
}
