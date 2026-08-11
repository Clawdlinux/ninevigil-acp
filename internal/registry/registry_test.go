/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package registry

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Clawdlinux/agent-native-format/pkg/manifest"
)

func newTool(id string, caps ...string) Tool {
	return Tool{
		ID:           id,
		Type:         "http",
		Endpoint:     "http://" + id,
		Method:       "POST",
		Schema:       map[string]string{"x": "string"},
		Auth:         manifest.AuthPreInjected,
		Capabilities: caps,
	}
}

func TestMemoryRegistry_Register_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tool    Tool
		wantErr bool
	}{
		{"valid", newTool("a", "sql"), false},
		{"empty id", Tool{Capabilities: []string{"sql"}}, true},
		{"whitespace id", Tool{ID: "  ", Capabilities: []string{"sql"}}, true},
		{"no capabilities", Tool{ID: "a"}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := NewMemoryRegistry().Register(tc.tool)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Register err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestMemoryRegistry_Get(t *testing.T) {
	t.Parallel()

	r := NewMemoryRegistry()
	if err := r.Register(newTool("db.query", "sql")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := r.Get("db.query")
	if err != nil {
		t.Fatalf("Get returned err: %v", err)
	}
	if got.ID != "db.query" {
		t.Fatalf("Get returned wrong tool: %#v", got)
	}

	if _, err := r.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRegistry_Lookup(t *testing.T) {
	t.Parallel()

	r := NewMemoryRegistry()
	for _, tl := range []Tool{
		newTool("db.query", "sql"),
		newTool("slack.send", "messaging", "slack"),
		newTool("audit.log", "audit"),
	} {
		if err := r.Register(tl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	cases := []struct {
		name string
		caps []string
		want []string
	}{
		{"single capability", []string{"sql"}, []string{"db.query"}},
		{"case insensitive", []string{"SQL"}, []string{"db.query"}},
		{"multiple capabilities", []string{"sql", "audit"}, []string{"audit.log", "db.query"}},
		{"alias matches", []string{"slack"}, []string{"slack.send"}},
		{"unknown capability", []string{"unknown"}, []string{}},
		{"empty input", nil, nil},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := r.Lookup(tc.caps)

			ids := make([]string, 0, len(got))
			for _, tl := range got {
				ids = append(ids, tl.ID)
			}
			if tc.want == nil && len(ids) != 0 {
				t.Fatalf("expected no matches, got %v", ids)
			}
			if tc.want != nil && !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("Lookup ids = %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestMemoryRegistry_AllSorted(t *testing.T) {
	t.Parallel()

	r := NewMemoryRegistry()
	for _, id := range []string{"c.tool", "a.tool", "b.tool"} {
		if err := r.Register(newTool(id, "x")); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got := r.All()
	if len(got) != 3 {
		t.Fatalf("All len = %d, want 3", len(got))
	}
	if got[0].ID != "a.tool" || got[1].ID != "b.tool" || got[2].ID != "c.tool" {
		t.Fatalf("All not sorted: %v", got)
	}
}

func TestSeed_PopulatesExpectedTools(t *testing.T) {
	t.Parallel()

	r := NewMemoryRegistry()
	if err := Seed(r); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	wantIDs := []string{"audit.log_event", "db.query", "email.send", "slack.send_message", "template.render"}
	all := r.All()
	if len(all) != len(wantIDs) {
		t.Fatalf("Seed registered %d tools, want %d", len(all), len(wantIDs))
	}
	for i, id := range wantIDs {
		if all[i].ID != id {
			t.Fatalf("Seed tool[%d] = %s, want %s", i, all[i].ID, id)
		}
	}
}
