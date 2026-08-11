/*
Copyright 2026 Clawdlinux.

Licensed under the Business Source License 1.1.
See LICENSE in the repository root.
*/

package proxy

import (
	"context"
	"testing"
)

func TestAlwaysApprove_AlwaysReturnsTrue(t *testing.T) {
	t.Parallel()
	gate := AlwaysApprove{}
	if !gate.IsApproved(context.Background(), "any-mid", "any-aid") {
		t.Fatal("AlwaysApprove must approve")
	}
}

func TestAlwaysDeny_AlwaysReturnsFalse(t *testing.T) {
	t.Parallel()
	gate := AlwaysDeny{}
	if gate.IsApproved(context.Background(), "any-mid", "any-aid") {
		t.Fatal("AlwaysDeny must deny")
	}
}
