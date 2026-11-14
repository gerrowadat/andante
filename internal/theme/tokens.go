// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Modes are the colour schemes a theme gives colours for. Pico picks one from the
// browser's prefers-color-scheme.
var Modes = []string{"light", "dark"}

// colours are the colour tokens a theme can set, under color.light and color.dark,
// and the Pico variables each one sets. A variable with an alpha gets the colour
// made that much more transparent.
var colours = []colour{
	{"background", []picoVar{{"--pico-background-color", 1}, {"--pico-form-element-active-background-color", 1}}},
	{"surface", []picoVar{
		{"--pico-card-background-color", 1}, {"--pico-card-sectioning-background-color", 1}, {"--pico-dropdown-background-color", 1},
		{"--pico-form-element-background-color", 1}, {"--pico-code-background-color", 1},
	}},
	{"text", []picoVar{
		{"--pico-color", 1}, {"--pico-h1-color", 1}, {"--pico-h2-color", 1}, {"--pico-h3-color", 1}, {"--pico-h4-color", 1},
		{"--pico-h5-color", 1}, {"--pico-h6-color", 1}, {"--pico-form-element-color", 1}, {"--pico-dropdown-color", 1},
	}},
	{"muted", []picoVar{{"--pico-muted-color", 1}, {"--pico-code-color", 1}}},
	{"border", []picoVar{{"--pico-muted-border-color", 1}, {"--pico-form-element-border-color", 1}}},
	{"primary", []picoVar{
		{"--pico-primary", 1}, {"--pico-primary-background", 1}, {"--pico-primary-underline", 0.5},
		{"--pico-primary-focus", 0.5}, {"--pico-text-selection-color", 0.25},
	}},
	{"primary-hover", []picoVar{{"--pico-primary-hover", 1}, {"--pico-primary-hover-background", 1}}},
	{"primary-inverse", []picoVar{{"--pico-primary-inverse", 1}}},
}

// colour is a colour token and the Pico variables it sets.
type colour struct {
	Name string
	vars []picoVar
}

type picoVar struct {
	name  string
	alpha float64
}

// dimension is a dimension token's limits, in rem, and its Pico variable.
type dimension struct {
	min, max float64
	pico     string
}

var dimensions = map[string]dimension{
	"size.radius":  {0, 4, "--pico-border-radius"},
	"size.spacing": {0.25, 4, "--pico-spacing"},
}

// Tokens lists every token a theme can set, with its type.
func Tokens() map[string]string {
	t := map[string]string{"font.body": "fontFamily"}
	for p := range dimensions {
		t[p] = "dimension"
	}
	for _, m := range Modes {
		for _, c := range colours {
			t["color."+m+"."+c.Name] = "color"
		}
	}
	return t
}

// remPerPx is the browser default, which Pico doesn't change.
const remPerPx = 1.0 / 16

// RGBA is a colour in sRGB, each part 0 to 1.
type RGBA struct{ R, G, B, A float64 }

// css writes the colour with its alpha multiplied by alpha.
func (c RGBA) css(alpha float64) string {
	b := func(v float64) int { return int(math.Round(v * 255)) }
	a := c.A * alpha
	if a >= 1 {
		return fmt.Sprintf("rgb(%d %d %d)", b(c.R), b(c.G), b(c.B))
	}
	return fmt.Sprintf("rgb(%d %d %d / %s)", b(c.R), b(c.G), b(c.B), num(a))
}

func num(v float64) string { return strconv.FormatFloat(math.Round(v*10000)/10000, 'f', -1, 64) }

var errAlias = errors.New("refers to another token, which andante doesn't follow yet: give the value itself")

// isAlias says whether a value is a reference to another token, such as "{color.base}",
// or an object with a $ref.
func isAlias(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		_, ok := m["$ref"]
		return ok
	}
	return false
}

func strict(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

var hexRE = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ParseColour reads a color token's value: the 2025.10 object, in the srgb colour
// space, or a hex string as earlier drafts of the format had it.
func ParseColour(raw json.RawMessage) (RGBA, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return parseHex(s)
	}
	var v struct {
		ColorSpace string    `json:"colorSpace"`
		Components []float64 `json:"components"`
		Alpha      *float64  `json:"alpha"`
		Hex        string    `json:"hex"`
	}
	if err := strict(raw, &v); err != nil {
		return RGBA{}, fmt.Errorf("a colour is an object with colorSpace and components: %w", err)
	}
	if v.ColorSpace != "srgb" {
		return RGBA{}, fmt.Errorf("colour space %q: andante takes srgb", v.ColorSpace)
	}
	if len(v.Components) != 3 {
		return RGBA{}, fmt.Errorf("an srgb colour has 3 components, not %d", len(v.Components))
	}
	c := RGBA{R: v.Components[0], G: v.Components[1], B: v.Components[2], A: 1}
	if v.Alpha != nil {
		c.A = *v.Alpha
	}
	for _, x := range []float64{c.R, c.G, c.B, c.A} {
		if x < 0 || x > 1 || math.IsNaN(x) {
			return RGBA{}, fmt.Errorf("components and alpha are 0 to 1, and %v isn't", x)
		}
	}
	if v.Hex != "" && !hexRE.MatchString(v.Hex) {
		return RGBA{}, fmt.Errorf("hex %q isn't #rrggbb", v.Hex)
	}
	return c, nil
}

func parseHex(s string) (RGBA, error) {
	if !hexRE.MatchString(s) {
		return RGBA{}, fmt.Errorf("%q isn't a colour: use the colorSpace and components object, or #rrggbb", s)
	}
	part := func(i int) float64 {
		n, _ := strconv.ParseUint(s[i:i+2], 16, 8) // The pattern has checked it.
		return float64(n) / 255
	}
	c := RGBA{R: part(1), G: part(3), B: part(5), A: 1}
	if len(s) == 9 {
		c.A = part(7)
	}
	return c, nil
}

// parseDimension reads a dimension token's value, in rem.
func parseDimension(raw json.RawMessage) (float64, error) {
	var v struct {
		Value *float64 `json:"value"`
		Unit  string   `json:"unit"`
	}
	if err := strict(raw, &v); err != nil || v.Value == nil {
		return 0, errors.New("a dimension is an object with a value and a unit")
	}
	switch v.Unit {
	case "rem":
		return *v.Value, nil
	case "px":
		return *v.Value * remPerPx, nil
	}
	return 0, fmt.Errorf("unit %q: a dimension is in px or rem", v.Unit)
}

var (
	familyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{0,63}$`)
	generic  = []string{
		"serif", "sans-serif", "monospace", "cursive", "fantasy", "system-ui", "ui-serif", "ui-sans-serif",
		"ui-monospace", "ui-rounded", "math", "emoji",
	}
)

const maxFamilies = 8

// parseFontFamily reads a fontFamily token's value, one name or a list of them, and
// writes it as a CSS font-family list.
func parseFontFamily(raw json.RawMessage) (string, error) {
	var names []string
	var one string
	if json.Unmarshal(raw, &one) == nil {
		names = []string{one}
	} else if err := json.Unmarshal(raw, &names); err != nil {
		return "", errors.New("a font family is a name or a list of names")
	}
	if len(names) == 0 || len(names) > maxFamilies {
		return "", fmt.Errorf("a font family has 1 to %d names", maxFamilies)
	}
	out := make([]string, len(names))
	for i, n := range names {
		if !familyRE.MatchString(n) {
			return "", fmt.Errorf("font %q: a name is letters, digits, spaces and hyphens", n)
		}
		if slices.Contains(generic, n) {
			out[i] = n
		} else {
			out[i] = `"` + n + `"`
		}
	}
	return strings.Join(out, ", "), nil
}
