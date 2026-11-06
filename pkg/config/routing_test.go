package config

import "testing"

func TestCheckRoutingConfig(t *testing.T) {
	cases := []struct {
		provider, key string
		wantErr       string
	}{
		{"", "", ""},
		{"none", "", ""},
		{"openrouteservice", "k", ""},
		{" OpenRouteService ", "k", ""},
		{"openrouteservice", "", routingKeyErr},
		{"osrm", "k", routingProviderErr},
	}
	for _, c := range cases {
		t.Setenv("ROUTING_PROVIDER", c.provider)
		t.Setenv("ORS_API_KEY", c.key)
		err := checkRoutingConfig()
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("provider %q key %q: unexpected error %v", c.provider, c.key, err)
		case c.wantErr != "" && (err == nil || err.Error() != c.wantErr):
			t.Errorf("provider %q key %q: err = %v, want %q", c.provider, c.key, err, c.wantErr)
		}
	}
}
