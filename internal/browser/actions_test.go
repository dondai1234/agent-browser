package browser

import "testing"

// TestValidateKeyPress covers the press_key input rule without a browser. The
// agent in the live OpenCode test passed key="weather in tokyo" (a whole string)
// and press_key silently no-op'd (a keyDown with a multi-char key string fires
// no native default + inserts nothing). The rule turns that into a clear error
// that redirects to fill/act.
func TestValidateKeyPress(t *testing.T) {
	cases := []struct {
		key     string
		wantErr bool
	}{
		{"Enter", false},      // named key
		{"Escape", false},     // named key
		{"Tab", false},        // named key
		{"ArrowDown", false},  // named key
		{"a", false},          // single char
		{"1", false},          // single char
		{"/", false},          // single char
		{" ", false},          // single space char (Space is also named; either path is fine)
		{"A", false},          // single uppercase char
		{"", true},            // empty
		{"weather in tokyo", true},   // the exact failure from the live test
		{"abc", true},         // multi-char
		{"Enter+shift", true}, // not a named key, not single char (modifiers go via the modifiers field)
		{"ctrl", true},        // modifier names are NOT keys
	}
	for _, c := range cases {
		err := validateKeyPress(c.key)
		if c.wantErr && err == nil {
			t.Errorf("validateKeyPress(%q): want error, got nil", c.key)
		}
		if !c.wantErr && err != nil {
			t.Errorf("validateKeyPress(%q): want nil, got %v", c.key, err)
		}
	}
}
