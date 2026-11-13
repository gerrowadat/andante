// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"

	"github.com/oapi-codegen/nullable"
)

const defaultLimit = 50

// page is a request for one page of a list ordered by a numeric key.
type page struct {
	after int64
	limit int32
}

// parsePage decodes a cursor, which is opaque to clients. When there is none the
// list starts after first.
func parsePage(cursor *string, limit *int32, first int64) (page, error) {
	p := page{after: first, limit: defaultLimit}
	if limit != nil {
		p.limit = *limit
	}
	if cursor == nil {
		return p, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*cursor)
	if err != nil {
		return page{}, invalidRequest("cursor is not valid")
	}
	if p.after, err = strconv.ParseInt(string(raw), 10, 64); err != nil {
		return page{}, invalidRequest("cursor is not valid")
	}
	return p, nil
}

// fetchLimit asks for one row more than the page holds, to tell whether there is
// another page.
func (p page) fetchLimit() int32 { return p.limit + 1 }

// paginated is one page of a list, with links to the next.
type paginated[T any] struct {
	items []T
	next  nullable.Nullable[string]
	link  *string
}

// paginate trims rows to the page and works out the next link. key returns a
// row's position in the list.
func paginate[R, T any](p page, path string, rows []R, key func(R) int64, convert func(R) (T, error)) (paginated[T], error) {
	result := paginated[T]{items: make([]T, 0, min(len(rows), int(p.limit))), next: nullable.NewNullNullable[string]()}
	more := len(rows) > int(p.limit)
	if more {
		rows = rows[:p.limit]
	}
	for _, row := range rows {
		item, err := convert(row)
		if err != nil {
			return paginated[T]{}, err
		}
		result.items = append(result.items, item)
	}
	if more {
		result.next, result.link = nextPage(path, nil, key(rows[len(rows)-1]), p.limit)
	}
	return result, nil
}

// nextPage returns the link to the page after the item at last, as a next link and a
// Link header. query holds any filters the next page keeps.
func nextPage(path string, query url.Values, last int64, limit int32) (nullable.Nullable[string], *string) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("cursor", base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last, 10))))
	query.Set("limit", strconv.Itoa(int(limit)))
	next := BasePath + path + "?" + query.Encode()
	link := fmt.Sprintf("<%s>; rel=\"next\"", next)
	return nullable.NewNullableWithValue(next), &link
}
