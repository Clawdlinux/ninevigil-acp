/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package resolver

import (
	"sync"
	"testing"
)

func TestDeferredResolver_ConcurrentObserveAndResolve(t *testing.T) {
	t.Parallel()

	allCaps := []string{"audit", "database", "email", "messaging", "sql", "template"}
	r := NewDeferredResolver(DeferredOptions{
		AllCapabilities: allCaps,
		WindowSize:      20,
		NarrowThreshold: 5,
	})

	const (
		observers = 8
		resolvers = 8
		opsEach   = 200
	)

	var wg sync.WaitGroup
	wg.Add(observers + resolvers)

	// Writers: concurrent Observe calls.
	for i := 0; i < observers; i++ {
		go func(id int) {
			defer wg.Done()
			cap := allCaps[id%len(allCaps)]
			for j := 0; j < opsEach; j++ {
				r.Observe(cap)
			}
		}(i)
	}

	// Readers: concurrent Resolve calls.
	for i := 0; i < resolvers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsEach; j++ {
				got, _ := r.Resolve("query database", nil)
				if got != nil {
					// Result should always be sorted.
					for k := 1; k < len(got); k++ {
						if got[k] < got[k-1] {
							t.Errorf("unsorted: %v", got)
							return
						}
					}
				}
			}
		}()
	}

	wg.Wait()

	// Reset is also safe under concurrency.
	wg.Add(2)
	go func() { defer wg.Done(); r.Reset() }()
	go func() { defer wg.Done(); r.Resolve("query", nil) }() //nolint:errcheck
	wg.Wait()
}
