package snapshot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
)

// formatElement renders one element as a dense ref-line, e.g.:
//
//	[r1] button "Submit"
//	[r3] textbox "Email" ="hello@x.com" [required]
//	[r7] heading "Welcome" [h1]
func formatElement(el Element) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", el.Ref, el.Role)
	if el.Name != "" {
		fmt.Fprintf(&b, " %q", el.Name)
	}
	if el.Value != "" &&
		(el.Role == "textbox" || el.Role == "searchbox" || el.Role == "combobox" || el.Role == "spinbutton") {
		fmt.Fprintf(&b, " =%q", el.Value)
	}
	var flags []string
	if el.Props[accessibility.PropertyNameDisabled] == "true" {
		flags = append(flags, "disabled")
	}
	if el.Props[accessibility.PropertyNameRequired] == "true" {
		flags = append(flags, "required")
	}
	if el.Props[accessibility.PropertyNameExpanded] == "true" {
		flags = append(flags, "expanded")
	}
	if el.Props[accessibility.PropertyNameSelected] == "true" {
		flags = append(flags, "selected")
	}
	if el.Props[accessibility.PropertyNameReadonly] == "true" {
		flags = append(flags, "readonly")
	}
	switch el.Props[accessibility.PropertyNameChecked] {
	case "true":
		flags = append(flags, "checked")
	case "mixed":
		flags = append(flags, "checked=mixed")
	}
	if el.Props[accessibility.PropertyNamePressed] == "true" {
		flags = append(flags, "pressed")
	}
	if el.Role == "heading" {
		if lvl := el.Props[accessibility.PropertyNameLevel]; lvl != "" {
			flags = append(flags, "h"+lvl)
		}
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(flags, " "))
	}
	if el.Frame != "" {
		fmt.Fprintf(&b, " in %q", el.Frame)
	}
	return b.String()
}

// propsOf flattens a node's Properties list into a name->string map.
func propsOf(n *accessibility.Node) map[accessibility.PropertyName]string {
	out := map[accessibility.PropertyName]string{}
	for _, p := range n.Properties {
		if p == nil {
			continue
		}
		out[p.Name] = axString(p.Value)
	}
	return out
}

// axString safely extracts a string from an *accessibility.Value. Value.Value
// is jsontext.Value (raw JSON bytes), so we decode it defensively into any
// and type-switch. Returns "" for nil/empty.
func axString(v *accessibility.Value) string {
	if v == nil || len(v.Value) == 0 {
		return ""
	}
	var raw any
	if err := json.Unmarshal(v.Value, &raw); err != nil {
		return ""
	}
	switch t := raw.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
