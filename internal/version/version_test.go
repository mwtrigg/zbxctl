package version

import "testing"

func TestFromModule(t *testing.T) {
	for in, want := range map[string]string{
		"v1.2.0":                               "1.2.0",
		"(devel)":                              "dev",
		"":                                     "dev",
		"v1.2.1-0.20261117120000-abcdef123456": "dev",
		"v1.2.0+dirty":                         "dev",
	} {
		if got := fromModule(in); got != want {
			t.Errorf("fromModule(%q) = %q, want %q", in, got, want)
		}
	}
}
