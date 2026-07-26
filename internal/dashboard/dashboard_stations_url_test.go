package dashboard

import "testing"

// Regression: a base_url typed with a trailing slash (e.g. "https://r.example/")
// must be normalized to no trailing slash, otherwise every polling request is
// built as base + "/v1/..." and the double slash 404s the upstream relay.
func TestStationInputNormalizesBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://r.example/", "https://r.example"},
		{"https://r.example//", "https://r.example"},
		{"  https://r.example/  ", "https://r.example"},
		{"https://r.example", "https://r.example"},          // already clean
		{"https://r.example/sub/", "https://r.example/sub"}, // path kept, trailing slash dropped
	}
	for _, c := range cases {
		in := stationInput{ID: "x", Name: "x", Kind: "newapi", BaseURL: c.in, PollInterval: "30s"}
		in.Auth.APIKey = "sk-test"
		st, err := in.toStation()
		if err != nil {
			t.Fatalf("toStation(%q): %v", c.in, err)
		}
		if st.BaseURL != c.want {
			t.Errorf("toStation(%q): got %q want %q", c.in, st.BaseURL, c.want)
		}
	}
}
