package testsupport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Response is a fully read HTTP response; its body has already been closed.
type Response struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

// Cookies returns the cookies set by the response.
func (r *Response) Cookies() []*http.Cookie { return r.cookies }

// Call sends a request and returns the response with its fully read body.
// A string body is sent verbatim; any other non-nil body is JSON-encoded.
func Call(t testing.TB, c *http.Client, method, url string, body any, header http.Header) (*Response, []byte) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, data
}

// Cookie returns the Set-Cookie entry called name, or nil.
func Cookie(resp *Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ErrorCode extracts error.code from a standard error body.
func ErrorCode(t testing.TB, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return e.Error.Code
}
