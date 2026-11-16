package version

import "testing"

func TestStringDefault(t *testing.T) {
	// Test binaries report a "(devel)" main module version, so the default holds.
	if got := String(); got != "dev" {
		t.Errorf("String() = %q, want %q", got, "dev")
	}
}

func TestStringStamped(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.2.3"
	if got := String(); got != "1.2.3" {
		t.Errorf("String() = %q, want %q", got, "1.2.3")
	}
}
