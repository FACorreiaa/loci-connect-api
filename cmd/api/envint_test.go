package api

import "testing"

func TestEnvInt(t *testing.T) {
	t.Setenv("MC_TEST", "")
	if got := envInt("MC_TEST", 2); got != 2 {
		t.Fatalf("unset = %d, want default 2", got)
	}
	t.Setenv("MC_TEST", "3")
	if got := envInt("MC_TEST", 2); got != 3 {
		t.Fatalf("3 = %d", got)
	}
	for _, bad := range []string{"0", "-1", "two"} {
		t.Setenv("MC_TEST", bad)
		if got := envInt("MC_TEST", 2); got != 2 {
			t.Fatalf("%q = %d, want default", bad, got)
		}
	}
}
