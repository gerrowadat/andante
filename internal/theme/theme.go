// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package theme reads the website's themes, which are Design Tokens files (the W3C
// Design Tokens Community Group's format, 2025.10), and turns each into a stylesheet
// of Pico CSS variables.
//
// A theme can set a small fixed set of tokens, listed in Tokens. Anything else is
// refused, and every value is checked by its type, so a theme can't put arbitrary
// CSS in the stylesheet.
package theme

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Default is the theme a site has until an admin picks another: Pico as it comes.
const Default = "default"

// Ext is what a theme's file name ends with. The name before it is the theme's name.
const Ext = ".tokens.json"

//go:embed themes/*.tokens.json
var builtin embed.FS

// Theme is one theme, ready to serve.
type Theme struct {
	Name        string
	Description string
	// CSS is the stylesheet, which goes after Pico's.
	CSS []byte
	// ETag is a strong validator for CSS.
	ETag string
}

// Set is the themes a site can choose from.
type Set struct {
	themes []*Theme
}

// Lookup finds a theme by its name.
func (s *Set) Lookup(name string) (*Theme, bool) {
	for _, t := range s.themes {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// All is every theme, the default first and the rest by name.
func (s *Set) All() []*Theme { return slices.Clone(s.themes) }

// Builtin reads the themes embedded in the binary. The server calls it when it
// starts, so a theme that doesn't parse stops it there.
var Builtin = sync.OnceValues(func() (*Set, error) { return Load(builtin, "themes") })

// MustBuiltin is Builtin for code that runs after the server has checked them.
func MustBuiltin() *Set {
	s, err := Builtin()
	if err != nil {
		panic(err)
	}
	return s
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidName says whether name could be a theme's name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Load reads every *.tokens.json file in dir. There has to be a default.
func Load(fsys fs.FS, dir string) (*Set, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	s := &Set{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		t, err := Parse(strings.TrimSuffix(e.Name(), Ext), data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		s.themes = append(s.themes, t)
	}
	if _, ok := s.Lookup(Default); !ok {
		return nil, fmt.Errorf("no %s%s in %s", Default, Ext, dir)
	}
	sort.Slice(s.themes, func(i, j int) bool {
		a, b := s.themes[i].Name, s.themes[j].Name
		if (a == Default) != (b == Default) {
			return a == Default
		}
		return a < b
	})
	return s, nil
}

// token is one token found in a file, with its type worked out from its groups.
type token struct {
	path  string
	typ   string
	value json.RawMessage
}

// Parse reads one theme from a Design Tokens file.
func Parse(name string, data []byte) (*Theme, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("%q isn't a theme name: lower-case letters, digits and hyphens, starting with a letter", name)
	}
	root, err := object(data)
	if err != nil {
		return nil, err
	}
	t := &Theme{Name: name}
	if d, ok := root["$description"]; ok {
		if err := json.Unmarshal(d, &t.Description); err != nil {
			return nil, fmt.Errorf("$description: %w", err)
		}
	}
	var found []token
	if err := walk(root, nil, "", &found); err != nil {
		return nil, err
	}
	css, err := render(name, found)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(css)
	t.CSS, t.ETag = css, `"`+hex.EncodeToString(sum[:12])+`"`
	return t, nil
}

func object(data []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("expected an object")
	}
	return m, nil
}

var (
	groupKeys = []string{"$type", "$description", "$extensions", "$deprecated"}
	tokenKeys = []string{"$value", "$type", "$description", "$extensions", "$deprecated"}
)

// walk finds the tokens under a group. A group's $type applies to everything in it
// that doesn't say otherwise.
func walk(group map[string]json.RawMessage, at []string, typ string, out *[]token) error {
	where := strings.Join(at, ".")
	if raw, ok := group["$type"]; ok {
		if err := json.Unmarshal(raw, &typ); err != nil {
			return fmt.Errorf("%s: $type: %w", where, err)
		}
	}
	keys := make([]string, 0, len(group))
	for k := range group {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "$") {
			if !slices.Contains(groupKeys, k) && (k != "$schema" || len(at) > 0) {
				return fmt.Errorf("%s: %s isn't a property a group can have", orRoot(where), k)
			}
			continue
		}
		if strings.ContainsAny(k, "{}.") {
			return fmt.Errorf("%s: %q can't be a name: it has a brace or a dot in it", orRoot(where), k)
		}
		child, err := object(group[k])
		if err != nil {
			return fmt.Errorf("%s: %w", strings.Join(append(at, k), "."), err)
		}
		next := append(slices.Clone(at), k)
		if _, ok := child["$value"]; !ok {
			if err := walk(child, next, typ, out); err != nil {
				return err
			}
			continue
		}
		p := strings.Join(next, ".")
		ttyp := typ
		for ck, raw := range child {
			if !slices.Contains(tokenKeys, ck) {
				return fmt.Errorf("%s: %s isn't a property a token can have", p, ck)
			}
			if ck == "$type" {
				if err := json.Unmarshal(raw, &ttyp); err != nil {
					return fmt.Errorf("%s: $type: %w", p, err)
				}
			}
		}
		*out = append(*out, token{path: p, typ: ttyp, value: child["$value"]})
	}
	return nil
}

func orRoot(where string) string {
	if where == "" {
		return "the top level"
	}
	return where
}
