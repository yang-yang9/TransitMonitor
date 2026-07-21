package e2e

import "testing"

func TestRun(t *testing.T) {
	if err := Run(); err != nil {
		t.Fatalf("self-test failed: %v", err)
	}
}
