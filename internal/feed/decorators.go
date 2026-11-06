// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/store/db"
)

const (
	kindPage    = "page"
	kindEpisode = "episode"
	kindArticle = "article"
)

// ItemContext is what a decorator knows about the item it is decorating.
type ItemContext struct {
	Read    db.Read
	Entry   db.Entry
	Role    string
	Token   string
	BaseURL string
}

// Decorator adds content to a feed item. Each declares what it applies to, so the
// core never switches on kind.
type Decorator interface {
	Applies(entry db.Entry) bool
	Decorate(ctx ItemContext, item *Item)
}

// decorators run in this order.
var decorators = []Decorator{
	imageDecorator{},
	contentDecorator{},
	enclosureDecorator{},
	originDecorator{},
	controlsDecorator{},
}

// imageDecorator shows a page's images, hotlinked and never proxied. Alt text goes
// underneath as text, because most readers do not show a title attribute.
type imageDecorator struct{}

func (imageDecorator) Applies(entry db.Entry) bool {
	return entry.Kind == kindPage && len(entry.Images) > 0
}

func (imageDecorator) Decorate(ctx ItemContext, item *Item) {
	alt := ""
	if ctx.Entry.AltText != nil {
		alt = *ctx.Entry.AltText
	}
	var b strings.Builder
	for _, image := range ctx.Entry.Images {
		fmt.Fprintf(&b, `<p><img src="%s" alt="%s"></p>`, html.EscapeString(image), html.EscapeString(alt))
	}
	if alt != "" {
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(alt))
	}
	item.ContentHTML += b.String()
}

// contentDecorator shows an article's text, or its summary and a way to the rest.
type contentDecorator struct{}

func (contentDecorator) Applies(entry db.Entry) bool {
	return entry.Kind == kindArticle || (entry.ContentHtml != nil && *entry.ContentHtml != "")
}

func (contentDecorator) Decorate(ctx ItemContext, item *Item) {
	// Content was sanitised when it was stored, so it goes in as it is.
	if ctx.Entry.ContentHtml != nil && *ctx.Entry.ContentHtml != "" {
		item.ContentHTML += *ctx.Entry.ContentHtml
		return
	}
	if ctx.Entry.Summary != nil {
		item.ContentHTML += "<p>" + html.EscapeString(*ctx.Entry.Summary) + "</p>"
	}
	if item.Link != "" {
		item.ContentHTML += fmt.Sprintf(`<p><a href="%s">Read on the original site</a></p>`, html.EscapeString(item.Link))
	}
}

// enclosureDecorator attaches media, which each format renders natively, with show
// notes from the entry. URLs pass through unchanged.
type enclosureDecorator struct{}

func (enclosureDecorator) Applies(entry db.Entry) bool {
	return entry.Kind == kindEpisode || len(enclosures(entry)) > 0
}

func enclosures(entry db.Entry) []Enclosure {
	var out []Enclosure
	// As with extra, a bad value loses that and no more.
	_ = json.Unmarshal(entry.Enclosures, &out)
	return out
}

func (enclosureDecorator) Decorate(ctx ItemContext, item *Item) {
	item.Enclosures = enclosures(ctx.Entry)
	if ctx.Entry.Duration.Valid {
		item.Duration = time.Duration(ctx.Entry.Duration.Microseconds)*time.Microsecond +
			time.Duration(ctx.Entry.Duration.Days)*24*time.Hour
	}
	item.Image = text(item.Extra, "itunes:image")
	if item.ContentHTML == "" && ctx.Entry.Summary != nil {
		item.ContentHTML = "<p>" + html.EscapeString(*ctx.Entry.Summary) + "</p>"
	}
}

// originDecorator says when and where the entry first appeared. It is what keeps a
// replayed item honest, because the item's own date is its release time.
type originDecorator struct{}

func (originDecorator) Applies(entry db.Entry) bool {
	return entry.OriginalDate != nil || entry.Url != nil
}

func (originDecorator) Decorate(ctx ItemContext, item *Item) {
	var b strings.Builder
	b.WriteString("<p>")
	if ctx.Entry.OriginalDate != nil {
		fmt.Fprintf(&b, "Originally published %s. ", ctx.Entry.OriginalDate.UTC().Format("2 January 2006"))
	}
	if item.Link != "" {
		fmt.Fprintf(&b, `<a href="%s">Original page</a>`, html.EscapeString(item.Link))
	}
	b.WriteString("</p>")
	item.ContentHTML += b.String()
}

// controlsDecorator gives the owner a way to release the next entry. A viewer's
// feed never contains it, because that link is the owner's token.
type controlsDecorator struct{}

func (controlsDecorator) Applies(db.Entry) bool { return true }

func (controlsDecorator) Decorate(ctx ItemContext, item *Item) {
	if ctx.Role != RoleOwner {
		return
	}
	next := strings.TrimRight(ctx.BaseURL, "/") + "/f/" + ctx.Token + "/next"
	item.ContentHTML += fmt.Sprintf(`<p><a href="%s">next one now →</a></p>`, html.EscapeString(next))
}
