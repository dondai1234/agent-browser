package snapshot

import "strings"

// Find returns elements matching a role and/or name substring (case-insensitive).
// Intent-first: get exactly the part you want without paying for the whole tree.
func (t *Tree) Find(role, text string) []Element {
	role = strings.ToLower(strings.TrimSpace(role))
	needle := strings.ToLower(strings.TrimSpace(text))
	var out []Element
	for _, el := range t.Elems {
		if role != "" && el.Role != role {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(el.Name), needle) {
			continue
		}
		out = append(out, el)
	}
	return out
}

// FindExact returns elements whose name equals text exactly (case-insensitive).
// Use this when substring matching is too loose (e.g. "more" matching both
// "More" and "...more than 100 firms...").
func (t *Tree) FindExact(role, text string) []Element {
	role = strings.ToLower(strings.TrimSpace(role))
	want := strings.ToLower(strings.TrimSpace(text))
	var out []Element
	for _, el := range t.Elems {
		if role != "" && el.Role != role {
			continue
		}
		if want != "" && strings.ToLower(el.Name) != want {
			continue
		}
		out = append(out, el)
	}
	return out
}

// ByRef returns the element with the given ref (e.g. "r46"), used to resolve a
// ref from a previous snapshot/find into the node to act on.
func (t *Tree) ByRef(ref string) (Element, bool) {
	for i := range t.Elems {
		if t.Elems[i].Ref == ref {
			return t.Elems[i], true
		}
	}
	return Element{}, false
}

// RenderElements renders a slice of elements (used by find results).
func RenderElements(els []Element) string {
	lines := make([]string, 0, len(els))
	for _, el := range els {
		lines = append(lines, formatElement(el))
	}
	return strings.Join(lines, "\n")
}
