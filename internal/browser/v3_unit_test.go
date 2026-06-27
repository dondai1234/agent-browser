package browser

import "testing"

func TestReturnReDetectsBody(t *testing.T) {
	cases := map[string]bool{
		"return {a:1}":           true,
		"const x=1; return x":    true,
		"document.title":         false,
		"$$('a').map(a=>a.href)": false,
		"return":                 true,
		"await wait(()=>$(sel))": false,
	}
	for script, want := range cases {
		if got := returnRe.MatchString(script); got != want {
			t.Errorf("returnRe.MatchString(%q)=%v want %v", script, got, want)
		}
	}
}

func TestParseErrObject(t *testing.T) {
	msg, ok := parseErrObject([]byte(`{"__error":"boom: bad selector"}`))
	if !ok || msg != "boom: bad selector" {
		t.Fatalf("want ok+\"boom: bad selector\", got ok=%v msg=%q", ok, msg)
	}
	// A normal data object is NOT an error.
	if _, ok := parseErrObject([]byte(`{"stars":"12.7k","issues":"243"}`)); ok {
		t.Fatalf("a data object must not be treated as an error")
	}
	// An __error with empty message is not surfaced (the script returned it
	// deliberately as data, or a throw with no message - don't mask as error).
	if _, ok := parseErrObject([]byte(`{"__error":""}`)); ok {
		t.Fatalf("empty __error should not be treated as an error")
	}
	// Malformed JSON is not an error.
	if _, ok := parseErrObject([]byte(`not json`)); ok {
		t.Fatalf("malformed JSON should not be treated as an error")
	}
}

func TestParseErrObjectArray(t *testing.T) {
	// An array result is data, not an error.
	if _, ok := parseErrObject([]byte(`[1,2,3]`)); ok {
		t.Fatalf("array result must not be treated as an error")
	}
}
