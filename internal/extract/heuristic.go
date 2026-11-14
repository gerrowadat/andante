// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

// What navigation links say, once lower-cased and stripped of anything that isn't a
// letter. Arrows alone are left out: "»" means next on one site and last on another.
var (
	nextLabels  = []string{"next", "next page", "next comic", "next strip", "next episode", "next post", "next chapter"}
	prevLabels  = []string{"prev", "previous", "previous page", "previous comic", "previous strip", "previous episode", "previous post", "previous chapter"}
	firstLabels = []string{"first", "first page", "first comic", "first strip", "first episode", "first post", "start", "start here", "beginning"}
)

// Images in these are the site's furniture, not the page's.
const furniture = "header, footer, nav"

// heuristics is the last resort, for pages with no metadata and no markup a platform
// is known by. It reads what a person reads: a link that says "Next", and an image
// that says it's the comic.
func heuristics(doc *goquery.Document, pageURL string) Fields {
	return Fields{
		Next: navLink(doc, pageURL, nextLabels),
		Prev: navLink(doc, pageURL, prevLabels),
	}
}

// navLink returns the first link on the same site whose label is one of labels. The
// label is the link's text, else its title or aria-label, else the alt text of an
// image inside it, which is how many sites draw their buttons.
func navLink(doc *goquery.Document, pageURL string, labels []string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	var found string
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		if !slices.Contains(labels, label(a)) {
			return true
		}
		href := strings.TrimSpace(a.AttrOr("href", ""))
		target, err := base.Parse(href)
		if err != nil || href == "" || strings.HasPrefix(href, "#") || target.Host != base.Host ||
			(target.Scheme != "http" && target.Scheme != "https") {
			return true
		}
		found = href
		return false
	})
	return found
}

// isNav says whether a link is one of the navigation links, whose images are buttons.
func isNav(a *goquery.Selection) bool {
	if a.Length() == 0 {
		return false
	}
	l := label(a)
	return slices.Contains(nextLabels, l) || slices.Contains(prevLabels, l) || slices.Contains(firstLabels, l)
}

func label(a *goquery.Selection) string {
	for _, text := range []string{
		a.Text(), a.AttrOr("title", ""), a.AttrOr("aria-label", ""), a.Find("img[alt]").First().AttrOr("alt", ""),
	} {
		if normalised := normalise(text); normalised != "" {
			return normalised
		}
	}
	return ""
}

// normalise lower-cases text, keeps only letters, and leaves single spaces between
// words, so "Next »" and " NEXT " both read "next".
func normalise(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	return strings.Join(words, " ")
}

// mainImage returns the first image whose class, id or path says it's the comic,
// leaving out the site's header, footer and navigation and the buttons in links.
func mainImage(doc *goquery.Document) string {
	var found string
	doc.Find("img[src]").EachWithBreak(func(_ int, img *goquery.Selection) bool {
		src := strings.TrimSpace(img.AttrOr("src", ""))
		if src == "" || strings.HasPrefix(src, "data:") || img.Closest(furniture).Length() > 0 || isNav(img.Closest("a")) {
			return true
		}
		path := src
		if u, err := url.Parse(src); err == nil {
			path = u.Path
		}
		for _, value := range []string{img.AttrOr("class", ""), img.AttrOr("id", ""), path} {
			if strings.Contains(strings.ToLower(value), "comic") {
				found = src
				return false
			}
		}
		return true
	})
	return found
}
