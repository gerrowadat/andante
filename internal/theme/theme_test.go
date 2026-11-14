// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package theme

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuiltinThemesLoad(t *testing.T) {
	set, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	all := set.All()
	if len(all) < 2 || all[0].Name != Default {
		t.Fatalf("themes = %v, want the default first and at least one more", names(all))
	}
	for _, th := range all {
		if th.Description == "" {
			t.Errorf("%s has no $description", th.Name)
		}
		if !strings.HasPrefix(th.ETag, `"`) || len(th.CSS) == 0 {
			t.Errorf("%s: etag %s, %d bytes of CSS", th.Name, th.ETag, len(th.CSS))
		}
	}
	paper, ok := set.Lookup("paper")
	if !ok {
		t.Fatal("no paper theme")
	}
	css := string(paper.CSS)
	for _, want := range []string{
		`--pico-font-family:"Iowan Old Style"`, ", serif", "--pico-border-radius:0.125rem",
		lightSelector + "{--pico-background-color:rgb(248 244 234)", "--pico-primary-underline:rgb(138 59 18 / 0.5)",
		darkMedia + "{" + darkAutoSelector + "{", darkSelector + "{",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("paper's CSS has no %q:\n%s", want, css)
		}
	}
	if _, ok := set.Lookup("nope"); ok {
		t.Error("found a theme that isn't there")
	}
}

func names(ts []*Theme) []string {
	var n []string
	for _, t := range ts {
		n = append(n, t.Name)
	}
	return n
}

// The built-in themes have to be readable: WCAG AA's 4.5:1 for text, links and
// button labels, in both modes.
func TestBuiltinThemesContrast(t *testing.T) {
	entries, err := builtin.ReadDir("themes")
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{
		{"text", "background"}, {"text", "surface"}, {"muted", "background"}, {"muted", "surface"},
		{"primary", "background"}, {"primary-inverse", "primary"}, {"primary-inverse", "primary-hover"},
	}
	for _, e := range entries {
		data, err := builtin.ReadFile("themes/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var found []token
		root, _ := object(data)
		if err := walk(root, nil, "", &found); err != nil {
			t.Fatal(err)
		}
		cols := map[string]RGBA{}
		for _, tok := range found {
			if tok.typ == "color" {
				c, err := ParseColour(tok.value)
				if err != nil {
					t.Fatal(err)
				}
				cols[strings.TrimPrefix(tok.path, "color.")] = c
			}
		}
		if len(cols) == 0 {
			continue // Pico's own.
		}
		for _, mode := range Modes {
			for _, p := range pairs {
				fg, ok1 := cols[mode+"."+p[0]]
				bg, ok2 := cols[mode+"."+p[1]]
				if !ok1 || !ok2 {
					t.Errorf("%s %s: missing %s or %s", e.Name(), mode, p[0], p[1])
					continue
				}
				if r := contrast(fg, bg); r < 4.5 {
					t.Errorf("%s %s: %s on %s is %.2f:1, under 4.5:1", e.Name(), mode, p[0], p[1], r)
				}
			}
		}
	}
}

func contrast(a, b RGBA) float64 {
	lum := func(c RGBA) float64 {
		ch := func(v float64) float64 {
			if v <= 0.04045 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func both(name, value string) string {
	return `{"color": {"$type": "color", "light": {"` + name + `": {"$value": ` + value + `}}, "dark": {"` + name + `": {"$value": ` + value + `}}}}`
}

func TestParse(t *testing.T) {
	ok := []struct{ name, doc, want string }{
		{"empty", `{}`, "/* The empty theme"},
		{"hex", both("text", `"#ff000080"`), "--pico-color:rgb(255 0 0 / 0.502)"},
		{"object", both("primary", `{"colorSpace": "srgb", "components": [0, 0, 1], "alpha": 0.5}`), "--pico-text-selection-color:rgb(0 0 255 / 0.125)"},
		{"px", `{"size": {"spacing": {"$type": "dimension", "$value": {"value": 8, "unit": "px"}}}}`, "--pico-spacing:0.5rem"},
		{"one-font", `{"font": {"body": {"$type": "fontFamily", "$value": "monospace"}}}`, "--pico-font-family:monospace}"},
		{"extras", `{"$schema": "x", "$description": "d", "$extensions": {"a": 1}, "font": {"$description": "f", "body": {"$type": "fontFamily", "$value": ["A B"], "$deprecated": false}}}`, `"A B"`},
	}
	for _, c := range ok {
		th, err := Parse(c.name, []byte(c.doc))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(string(th.CSS), c.want) {
			t.Errorf("%s: CSS has no %q:\n%s", c.name, c.want, th.CSS)
		}
	}

	bad := []struct{ name, doc, want string }{
		{"not-json", `[`, "unexpected"},
		{"unknown-token", `{"color": {"$type": "color", "light": {"shadow": {"$value": "#000000"}}}}`, "can't set this"},
		{"wrong-type", `{"font": {"body": {"$type": "color", "$value": "#000000"}}}`, "has to be fontFamily"},
		{"no-type", `{"font": {"body": {"$value": "serif"}}}`, "has to be fontFamily"},
		{"one-mode", `{"color": {"$type": "color", "light": {"text": {"$value": "#000000"}}}}`, "both light and dark"},
		{"alias", both("text", `"{color.base}"`), "refers to another token"},
		{"ref", both("text", `{"$ref": "#/color/base"}`), "refers to another token"},
		{"css-in-colour", both("text", `"red;}body{display:none"`), "isn't a colour"},
		{"space", both("text", `{"colorSpace": "oklch", "components": [0.5, 0.1, 30]}`), "andante takes srgb"},
		{"components", both("text", `{"colorSpace": "srgb", "components": [0, 0]}`), "3 components"},
		{"range", both("text", `{"colorSpace": "srgb", "components": [0, 0, 2]}`), "0 to 1"},
		{"none", both("text", `{"colorSpace": "srgb", "components": ["none", 0, 0]}`), "colorSpace and components"},
		{"extra-field", both("text", `{"colorSpace": "srgb", "components": [0, 0, 0], "style": "x"}`), "colorSpace and components"},
		{"bad-hex", both("text", `{"colorSpace": "srgb", "components": [0, 0, 0], "hex": "red"}`), "isn't #rrggbb"},
		{"unit", `{"size": {"radius": {"$type": "dimension", "$value": {"value": 1, "unit": "em"}}}}`, "px or rem"},
		{"too-big", `{"size": {"radius": {"$type": "dimension", "$value": {"value": 10, "unit": "rem"}}}}`, "out of range"},
		{"no-value", `{"size": {"radius": {"$type": "dimension", "$value": {"unit": "rem"}}}}`, "value and a unit"},
		{"css-in-font", `{"font": {"body": {"$type": "fontFamily", "$value": "x\";}body{"}}}`, "letters, digits"},
		{"no-fonts", `{"font": {"body": {"$type": "fontFamily", "$value": []}}}`, "1 to 8"},
		{"font-type", `{"font": {"body": {"$type": "fontFamily", "$value": 3}}}`, "name or a list"},
		{"group-key", `{"font": {"$value2": 1}}`, "isn't a property a group can have"},
		{"schema-deep", `{"font": {"$schema": "x"}}`, "isn't a property a group can have"},
		{"token-key", `{"font": {"body": {"$type": "fontFamily", "$value": "serif", "css": 1}}}`, "isn't a property a token can have"},
		{"dot", `{"font.body": {"$type": "fontFamily", "$value": "serif"}}`, "brace or a dot"},
		{"leaf", `{"font": 3}`, "font"},
		{"description", `{"$description": 3}`, "$description"},
		{"group-type", `{"font": {"$type": 3}}`, "$type"},
		{"token-type", `{"font": {"body": {"$type": 3, "$value": "serif"}}}`, "$type"},
		{"null", `null`, "expected an object"},
	}
	for _, c := range bad {
		_, err := Parse(c.name, []byte(c.doc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one with %q", c.name, err, c.want)
		}
	}
	if _, err := Parse("Bad Name", []byte(`{}`)); err == nil {
		t.Error("a bad name was allowed")
	}
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"t/default.tokens.json": {Data: []byte(`{}`)},
		"t/b.tokens.json":       {Data: []byte(`{}`)},
		"t/a.tokens.json":       {Data: []byte(`{}`)},
		"t/readme.md":           {Data: []byte(`no`)},
	}
	set, err := Load(fsys, "t")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(set.All()), ","); got != "default,a,b" {
		t.Errorf("order = %s", got)
	}
	delete(fsys, "t/default.tokens.json")
	if _, err := Load(fsys, "t"); err == nil || !strings.Contains(err.Error(), "no default") {
		t.Errorf("without a default: %v", err)
	}
	fsys["t/default.tokens.json"] = &fstest.MapFile{Data: []byte(`{"x": {"$value": 1}}`)}
	if _, err := Load(fsys, "t"); err == nil || !strings.Contains(err.Error(), "default.tokens.json: x") {
		t.Errorf("with a bad file: %v", err)
	}
	if _, err := Load(fsys, "missing"); err == nil {
		t.Error("a missing directory loaded")
	}
}

func TestTokensListed(t *testing.T) {
	tokens := Tokens()
	if tokens["font.body"] != "fontFamily" || tokens["size.radius"] != "dimension" || tokens["color.dark.primary-inverse"] != "color" {
		t.Errorf("tokens = %v", tokens)
	}
	// Every colour has a CSS variable to set.
	for _, c := range colours {
		if len(c.vars) == 0 {
			t.Errorf("%s sets nothing", c.Name)
		}
	}
	var v json.RawMessage
	if isAlias(v) {
		t.Error("nothing is an alias")
	}
}
