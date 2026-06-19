package browser

import "testing"

func TestValidateURLDefault(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"  https://x.com  ", "https://x.com"},
	}
	for _, c := range cases {
		got, err := ValidateURL(c.in, false)
		if err != nil {
			t.Errorf("ValidateURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ValidateURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateURLBlockedSchemes(t *testing.T) {
	blocked := []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,<x>",
		"about:blank",
		"blob:http://x/y",
	}
	for _, b := range blocked {
		if _, err := ValidateURL(b, false); err == nil {
			t.Errorf("expected %q blocked by default, got ok", b)
		}
	}
}

func TestValidateURLInsecureOptIn(t *testing.T) {
	if _, err := ValidateURL("file:///tmp/x", true); err != nil {
		t.Errorf("file with allowInsecure should pass: %v", err)
	}
}

func TestValidateURLEmptyAndRelative(t *testing.T) {
	if _, err := ValidateURL("", false); err == nil {
		t.Error("empty url should error")
	}
	if _, err := ValidateURL("relative/path", false); err == nil {
		t.Error("relative url should error (no scheme)")
	}
}
