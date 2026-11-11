package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// readJSONFile decodes a file of the repository, named from its top, into v.
func readJSONFile(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// The Claude Code plugin only wires hooks to the CLI: the marketplace at the
// top of the repository lists the plugin in plugin/, the plugin carries no
// version of its own, and its session start hook runs agb hook session-start
// without a shell, naming the tool whose hook actor agb setup claude saves.
func TestClaudePluginFiles(t *testing.T) {
	var market struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSONFile(t, ".claude-plugin/marketplace.json", &market)
	if market.Name != claudeMarketplace || len(market.Plugins) != 1 || market.Plugins[0].Name != claudePlugin || market.Plugins[0].Source != "./plugin" {
		t.Errorf("marketplace = %+v", market)
	}

	var manifest map[string]any
	readJSONFile(t, "plugin/.claude-plugin/plugin.json", &manifest)
	if manifest["name"] != claudePlugin {
		t.Errorf("plugin name = %v", manifest["name"])
	}
	if _, ok := manifest["version"]; ok {
		t.Errorf("plugin.json sets a version, which keeps installs from following new commits")
	}

	type hook struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Timeout int      `json:"timeout"`
	}
	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []hook `json:"hooks"`
		} `json:"hooks"`
	}
	readJSONFile(t, "plugin/hooks/hooks.json", &hooks)
	start := hooks.Hooks["SessionStart"]
	if len(hooks.Hooks) != 1 || len(start) != 1 || start[0].Matcher != "" {
		t.Fatalf("hooks = %+v", hooks.Hooks)
	}
	want := []hook{{Type: "command", Command: "agb", Args: []string{"hook", "session-start", "--tool", claudeTool}, Timeout: 10}}
	if !reflect.DeepEqual(start[0].Hooks, want) {
		t.Errorf("SessionStart hooks = %+v, want %+v", start[0].Hooks, want)
	}
	// The command the hook names exists.
	if sub := root.find(want[0].Args[0]); sub == nil || sub.find(want[0].Args[1]) == nil {
		t.Errorf("agb %v is not a command", want[0].Args)
	}
}
