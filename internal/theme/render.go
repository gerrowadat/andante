// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package theme

import (
	"fmt"
	"slices"
	"strings"
)

// Pico's own selectors for each mode, so that the theme's variables replace Pico's
// exactly where Pico sets them. The theme's stylesheet comes after Pico's.
const (
	lightSelector    = ":host(:not([data-theme=dark])),:root:not([data-theme=dark]),[data-theme=light]"
	darkMedia        = "@media only screen and (prefers-color-scheme:dark)"
	darkAutoSelector = ":host(:not([data-theme])),:root:not([data-theme])"
	darkSelector     = ":host([data-theme=dark]),[data-theme=dark]"
)

// render checks each token against what a theme can set and writes the stylesheet.
func render(name string, found []token) ([]byte, error) {
	allowed := Tokens()
	var root []string
	modes := map[string][]string{}
	has := map[string]bool{}
	for _, t := range found {
		want, ok := allowed[t.path]
		if !ok {
			return nil, fmt.Errorf("%s: a theme can't set this; it can set %s", t.path, known())
		}
		if t.typ != want {
			return nil, fmt.Errorf("%s: its $type has to be %s, not %q", t.path, want, t.typ)
		}
		if isAlias(t.value) {
			return nil, fmt.Errorf("%s: %w", t.path, errAlias)
		}
		has[t.path] = true
		switch want {
		case "fontFamily":
			f, err := parseFontFamily(t.value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", t.path, err)
			}
			root = append(root, "--pico-font-family:"+f)
		case "dimension":
			d := dimensions[t.path]
			rem, err := parseDimension(t.value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", t.path, err)
			}
			if rem < d.min || rem > d.max {
				return nil, fmt.Errorf("%s: %srem is out of range: %s to %srem", t.path, num(rem), num(d.min), num(d.max))
			}
			root = append(root, d.pico+":"+num(rem)+"rem")
		case "color":
			c, err := ParseColour(t.value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", t.path, err)
			}
			mode, which, _ := strings.Cut(strings.TrimPrefix(t.path, "color."), ".")
			for _, col := range colours {
				if col.Name != which {
					continue
				}
				for _, v := range col.vars {
					modes[mode] = append(modes[mode], v.name+":"+c.css(v.alpha))
				}
			}
		}
	}
	// A colour for one mode only would leave the other with Pico's, which may not go
	// with the rest of it.
	for _, c := range colours {
		light, dark := has["color.light."+c.Name], has["color.dark."+c.Name]
		if light != dark {
			return nil, fmt.Errorf("color.*.%s: a colour needs a value for both light and dark", c.Name)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "/* The %s theme, made from %s%s. */\n", name, name, Ext)
	block := func(selector string, decls []string) {
		if len(decls) > 0 {
			fmt.Fprintf(&b, "%s{%s}\n", selector, strings.Join(decls, ";"))
		}
	}
	block(":root", root)
	block(lightSelector, modes["light"])
	if dark := modes["dark"]; len(dark) > 0 {
		fmt.Fprintf(&b, "%s{%s{%s}}\n", darkMedia, darkAutoSelector, strings.Join(dark, ";"))
		block(darkSelector, dark)
	}
	return []byte(b.String()), nil
}

func known() string {
	var names []string
	for p := range Tokens() {
		names = append(names, p)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
