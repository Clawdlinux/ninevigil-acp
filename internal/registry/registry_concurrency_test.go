/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package registry

import (
	"fmt"
	"sync"
	"testing"
)

// TestMemoryRegistry_ConcurrentReadersAndWriters runs the registry under
// concurrent Register / Get / Lookup / All to flush out any data races the
// -race detector would otherwise miss because no test exercises the surface
// from multiple goroutines.
func TestMemoryRegistry_ConcurrentReadersAndWriters(t *testing.T) {
	t.Parallel()

	r := NewMemoryRegistry()
	if err := Seed(r); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const (
		writers    = 8
		readers    = 16
		writesEach = 200
		readsEach  = 500
	)

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for w := 0; w < writers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < writesEach; i++ {
				id := fmt.Sprintf("worker-%d-tool-%d", w, i)
				if err := r.Register(newTool(id, "stress")); err != nil {
					t.Errorf("Register: %v", err)
					return
				}
			}
		}()
	}

	for rId := 0; rId < readers; rId++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsEach; i++ {
				_ = r.Lookup([]string{"sql", "audit", "stress"})
				_ = r.All()
				if _, err := r.Get("db.query"); err != nil {
					t.Errorf("Get db.query: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// After the storm, the seeded tools must still be reachable and the stress
	// tools must all be present.
	all := r.All()
	if len(all) != 5+writers*writesEach {
		t.Fatalf("All count = %d, want %d", len(all), 5+writers*writesEach)
	}
	for _, must := range []string{"db.query", "email.send", "slack.send_message", "audit.log_event", "template.render"} {
		if _, err := r.Get(must); err != nil {
			t.Fatalf("seeded tool %q lost during stress: %v", must, err)
		}
	}
}
