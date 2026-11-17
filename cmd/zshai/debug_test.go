package main

import (
	"strings"
	"testing"
	"time"

	"github.com/MariusEng/zshai/internal/config"
	"github.com/MariusEng/zshai/internal/store"
)

func TestDebugReport(t *testing.T) {
	t.Setenv("ZSHAI_STATE_DIR", t.TempDir())
	t.Setenv("ZSHAI_CONFIG_DIR", t.TempDir())
	store.SaveTurn(store.Turn{UID: "20261116-1-abc", Started: time.Now(), Cwd: "/x", Request: "lag dummy.json", Script: "jq . > data.json",
		Model: "zshai-mini", Outcome: "refused", Problems: []string{"the test run created data.json, but it is empty"},
		SystemPrompt: "You are zshai-mini.",
		Responses:    []store.Response{{At: time.Now(), Prompt: "request: lag dummy.json", Response: "r: change\n---\njq . > data.json"}},
		Tests: []store.Test{{Command: "jq . > data.json", Exit: 2, Stderr: "jq: error: Could not open file dummy.json",
			Effects: []string{"created data.json (0 B)"}, Problems: []string{"a test run … failed (exit 2)"}}}})
	cfg := config.Defaults()
	cfg.APIKey = "secret-key"
	r := debugReport(cfg, []string{"20261116-1-abc"})
	for _, want := range []string{"## 1. lag dummy.json", "outcome: **refused**", "You are zshai-mini.", "### Model call 1",
		"request: lag dummy.json", "### Test run 1", "exit: 2", "Could not open file dummy.json", "effect: created data.json (0 B)",
		"the test run created data.json, but it is empty"} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(r, "secret-key") {
		t.Error("the API key is in the report")
	}
}
