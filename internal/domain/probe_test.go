package domain

import "testing"

func TestProbeConfig_TargetModels(t *testing.T) {
	cases := []struct {
		name string
		p    ProbeConfig
		want []string
	}{
		{"models list wins", ProbeConfig{Model: "single", Models: []string{"a", "b"}}, []string{"a", "b"}},
		{"fallback to single model", ProbeConfig{Model: "single"}, []string{"single"}},
		{"empty when neither set", ProbeConfig{}, nil},
		{"empty model + empty models", ProbeConfig{Model: "", Models: []string{}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.p.TargetModels()
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}
