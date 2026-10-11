package logto

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// MaxPageSize is the largest page_size the Management API accepts.
const MaxPageSize = 100

// Page selects one page of a paginated list. A zero Page means page 1.
type Page struct {
	Page     int
	PageSize int
}

func (p Page) apply(q url.Values) {
	page, size := p.Page, p.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(size))
}

// totalNumber reads the Total-Number response header, or -1 if absent.
func totalNumber(resp *Response) int {
	if resp == nil {
		return -1
	}
	n, err := strconv.Atoi(resp.Header.Get("Total-Number"))
	if err != nil {
		return -1
	}
	return n
}

// listPage fetches one page and returns the items plus the Total-Number header.
func listPage[T any](ctx context.Context, c *Client, path string, query url.Values, p Page) ([]T, int, error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = append([]string(nil), v...)
	}
	p.apply(q)
	var items []T
	resp, err := c.do(ctx, http.MethodGet, path, q, nil, &items)
	if err != nil {
		return nil, 0, err
	}
	return items, totalNumber(resp), nil
}

// listAll walks every page of a paginated endpoint. It stops on a short or
// empty page, once Total-Number items were read, or when the endpoint sends no
// Total-Number header (it ignored the page params and returned everything).
func listAll[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		items, total, err := listPage[T](ctx, c, path, query, Page{Page: page, PageSize: MaxPageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < MaxPageSize || total < 0 || len(all) >= total {
			return all, nil
		}
	}
}
