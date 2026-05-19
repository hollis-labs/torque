package federation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fedConfigFixture writes a valid federation identity and config map into a
// fresh temp dir and returns the dir plus the (mutable) config map. Tests
// mutate or delete keys to exercise validation.
func fedConfigFixture(t *testing.T) (dir string, cfg map[string]any) {
	t.Helper()
	dir = t.TempDir()
	writeIdentityPEM(t, dir, "fed", validIdentity(t))
	cfg = map[string]any{
		"listen_addr":       ":8443",
		"local_authorities": []any{"torque-a"},
		"identity":          map[string]any{"cert_file": "fed.crt", "key_file": "fed.key"},
		"peers": []any{
			map[string]any{
				"label":        "peer-b",
				"fingerprints": []any{strings.Repeat("ab", 32)},
				"authorities":  []any{"torque-b"},
			},
		},
		"foreign_routes": []any{
			map[string]any{
				"authority":   "torque-b",
				"endpoint":    "https://b.example:8443",
				"server_pins": []any{strings.Repeat("cd", 32)},
			},
		},
	}
	return dir, cfg
}

func writeConfig(t *testing.T, dir string, cfg map[string]any) string {
	t.Helper()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	p := filepath.Join(dir, "federation.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestLoadConfigAbsentFileMeansDisabled(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("absent config should not error: %v", err)
	}
	if c != nil {
		t.Fatal("absent config should yield a nil *Config (federation disabled)")
	}
}

func TestLoadConfigValid(t *testing.T) {
	dir, cfg := fedConfigFixture(t)
	c, err := LoadConfig(writeConfig(t, dir, cfg))
	if err != nil {
		t.Fatalf("LoadConfig of a valid config: %v", err)
	}
	if c == nil {
		t.Fatal("valid config yielded nil *Config")
	}
	if c.leaf == nil || c.peers == nil {
		t.Fatal("finalize did not populate the derived identity/peer fields")
	}
	if _, ok := c.peers.Lookup(strings.Repeat("ab", 32)); !ok {
		t.Error("peer registry missing the configured fingerprint")
	}

	routes, err := c.buildForeignRoutes()
	if err != nil {
		t.Fatalf("buildForeignRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Client == nil {
		t.Fatalf("foreign route not built with an mTLS client: %+v", routes)
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	cases := map[string]func(cfg map[string]any){
		"missing listen_addr":      func(c map[string]any) { delete(c, "listen_addr") },
		"empty local_authorities":  func(c map[string]any) { c["local_authorities"] = []any{} },
		"missing identity":         func(c map[string]any) { delete(c, "identity") },
		"peer without authorities": func(c map[string]any) { c["peers"].([]any)[0].(map[string]any)["authorities"] = []any{} },
		"foreign route http scheme": func(c map[string]any) {
			c["foreign_routes"].([]any)[0].(map[string]any)["endpoint"] = "http://b.example:8443"
		},
		"foreign route without pins": func(c map[string]any) {
			c["foreign_routes"].([]any)[0].(map[string]any)["server_pins"] = []any{}
		},
		"unknown key": func(c map[string]any) { c["bogus_key"] = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir, cfg := fedConfigFixture(t)
			mutate(cfg)
			if _, err := LoadConfig(writeConfig(t, dir, cfg)); err == nil {
				t.Errorf("%s: LoadConfig accepted an invalid config", name)
			}
		})
	}
}

func TestLoadConfigRejectsAuthorityBothLocalAndForeign(t *testing.T) {
	dir, cfg := fedConfigFixture(t)
	cfg["local_authorities"] = []any{"torque-a", "torque-b"} // torque-b is also a foreign route
	if _, err := LoadConfig(writeConfig(t, dir, cfg)); err == nil {
		t.Fatal("an authority both local and foreign-routed must be rejected")
	}
}
