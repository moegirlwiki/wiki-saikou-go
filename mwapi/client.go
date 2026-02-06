package mwapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type Option func(*Client)

func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.ua = ua
		}
	}
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.hc = hc
		}
	}
}

func WithTransport(rt http.RoundTripper) Option {
	return func(c *Client) {
		if c.hc == nil {
			return
		}
		if rt != nil {
			c.hc.Transport = rt
		}
	}
}

func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.hc == nil {
			return
		}
		if d > 0 {
			c.hc.Timeout = d
		}
	}
}

func WithThrowOnApiError(v bool) Option {
	return func(c *Client) {
		c.throwOnApiError = v
	}
}

func WithKeepLogin(v bool) Option {
	return func(c *Client) {
		c.keepLogin = v
	}
}

func WithReloginRetry(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.reloginRetry = n
		}
	}
}

func WithTokenRetry(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.tokenRetry = n
		}
	}
}

type Client struct {
	endpoint *url.URL
	hc       *http.Client
	ua       string

	throwOnApiError bool
	keepLogin       bool
	reloginRetry    int
	tokenRetry      int

	mu     sync.Mutex
	tokens map[TokenType]string
	_sf    *singleflight.Group

	loggedInUser string
	loginUser    string
	loginPass    string
}

func New(endpoint string, opts ...Option) *Client {
	c, err := NewClient(endpoint, opts...)
	if err != nil {
		panic(err)
	}
	return c
}

func NewClient(endpoint string, opts ...Option) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint URL (expect full URL): %q", endpoint)
	}
	if !strings.HasSuffix(u.Path, "api.php") {
		return nil, fmt.Errorf("invalid endpoint path (expect .../api.php): %q", u.Path)
	}

	jar, _ := cookiejar.New(nil)
	hc := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	c := &Client{
		endpoint:        u,
		hc:              hc,
		ua:              "mwapi-go/0.1",
		throwOnApiError: false,
		keepLogin:       true,
		reloginRetry:    3,
		tokenRetry:      3,
		tokens:          map[TokenType]string{},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	if c.hc == nil {
		c.hc = hc
	}
	if c.hc.Jar == nil {
		jar2, _ := cookiejar.New(nil)
		c.hc.Jar = jar2
	}

	return c, nil
}

func (c *Client) Get(ctx context.Context, p any) (*Response, error) {
	return c.do(ctx, http.MethodGet, p, doOptions{})
}

func (c *Client) Post(ctx context.Context, p any) (*Response, error) {
	return c.do(ctx, http.MethodPost, p, doOptions{})
}

type doOptions struct {
	skipAssert  bool
	skipRelogin bool
}

func (c *Client) do(ctx context.Context, method string, p any, opt doOptions) (*Response, error) {
	np, err := normalizeParams(p)
	if err != nil {
		return nil, err
	}

	action := strings.ToLower(np.Values.Get("action"))
	meta := strings.ToLower(np.Values.Get("meta"))
	typ := strings.ToLower(np.Values.Get("type"))

	// Keep-login: inject assertuser=username, but never for login or login-token.
	shouldSkipAssert := opt.skipAssert
	if action == "login" {
		shouldSkipAssert = true
	}
	if action == "query" && meta == "tokens" && strings.Contains(typ, "login") {
		shouldSkipAssert = true
	}
	if c.keepLogin && !shouldSkipAssert {
		c.mu.Lock()
		user := c.loggedInUser
		c.mu.Unlock()
		if user != "" && np.Values.Get("assertuser") == "" {
			np.Values.Set("assertuser", user)
		}
	}

	var lastErr error
	maxRelogin := 0
	if !opt.skipRelogin {
		maxRelogin = c.reloginRetry
	}

	for attempt := 0; attempt <= maxRelogin; attempt++ {
		resp, err := c.doOnce(ctx, method, np)
		if err == nil {
			if code := responseErrorCode(resp); isAssertUserFailedCode(code) && attempt < maxRelogin {
				lastErr = &MediaWikiApiError{
					Code:       code,
					Message:    "assertuser failed",
					HTTPStatus: resp.StatusCode,
					Response:   resp,
				}
				if err2 := c.Relogin(ctx); err2 != nil {
					return resp, errors.Join(lastErr, err2)
				}
				continue
			}
			if code := responseErrorCode(resp); isAssertUserFailedCode(code) && attempt == maxRelogin {
				return resp, &MediaWikiApiError{
					Code:       code,
					Message:    "assertuser failed",
					HTTPStatus: resp.StatusCode,
					Response:   resp,
				}
			}
			return resp, nil
		}
		lastErr = err

		e, ok := IsMediaWikiApiError(err)
		if !ok || e.Code == "" || !isAssertUserFailedCode(e.Code) {
			return resp, err
		}
		if attempt == maxRelogin {
			return resp, err
		}
		if err2 := c.Relogin(ctx); err2 != nil {
			return resp, errors.Join(err, err2)
		}
		// Retry the original request after relogin.
	}

	return nil, lastErr
}

func (c *Client) doOnce(ctx context.Context, method string, np normalizedParams) (*Response, error) {
	req, err := c.buildRequest(ctx, method, np)
	if err != nil {
		return nil, err
	}

	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	const maxBody = 32 << 20 // 32MiB
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, err
	}

	resp := &Response{
		StatusCode: res.StatusCode,
		Header:     res.Header.Clone(),
		Raw:        json.RawMessage(body),
	}

	// Best-effort parse the minimal envelope fields.
	_ = json.Unmarshal(body, &resp.Envelope)

	if c.throwOnApiError {
		if apiErr := responseApiError(resp); apiErr != nil {
			return resp, apiErr
		}
	}

	return resp, nil
}

func (c *Client) buildRequest(ctx context.Context, method string, np normalizedParams) (*http.Request, error) {
	base := *c.endpoint
	baseQuery := base.Query()

	if method == http.MethodGet {
		merged := mergeQuery(baseQuery, np.Values, nil)
		base.RawQuery = merged.Encode()
		req, err := http.NewRequestWithContext(ctx, method, base.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.ua)
		return req, nil
	}

	// POST: body wins if the same key exists in endpoint query.
	if len(np.Values) > 0 {
		for k := range np.Values {
			baseQuery.Del(k)
		}
	}
	base.RawQuery = baseQuery.Encode()

	var body io.Reader
	contentType := "application/x-www-form-urlencoded"

	if len(np.Files) == 0 {
		body = strings.NewReader(np.Values.Encode())
	} else {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, vs := range np.Values {
			if len(vs) == 0 {
				continue
			}
			_ = w.WriteField(k, vs[0])
		}
		for _, f := range np.Files {
			filename := f.File.Filename
			if filename == "" {
				filename = f.Field
			}
			fw, err := w.CreateFormFile(f.Field, filename)
			if err != nil {
				_ = w.Close()
				return nil, err
			}
			if _, err := io.Copy(fw, f.File.Reader); err != nil {
				_ = w.Close()
				return nil, err
			}
		}
		_ = w.Close()
		body = &buf
		contentType = w.FormDataContentType()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", c.ua)
	return req, nil
}

func mergeQuery(base url.Values, overlay url.Values, omitKeys map[string]struct{}) url.Values {
	out := url.Values{}
	for k, vs := range base {
		if omitKeys != nil {
			if _, ok := omitKeys[k]; ok {
				continue
			}
		}
		for _, v := range vs {
			out.Add(k, v)
		}
	}
	for k, vs := range overlay {
		if omitKeys != nil {
			if _, ok := omitKeys[k]; ok {
				continue
			}
		}
		// For safety, use Set (single value) for overlay.
		if len(vs) > 0 {
			out.Set(k, vs[0])
		}
	}
	return out
}

func responseApiError(r *Response) *MediaWikiApiError {
	if r.Error == nil && len(r.Errors) == 0 {
		return nil
	}
	var code, msg string
	var errs []MWError
	if r.Error != nil {
		code = r.Error.Code
		msg = firstNonEmpty(r.Error.Info, r.Error.Text)
		errs = append(errs, *r.Error)
	}
	if len(r.Errors) > 0 {
		if code == "" {
			code = r.Errors[0].Code
		}
		if msg == "" {
			msg = firstNonEmpty(r.Errors[0].Info, r.Errors[0].Text)
		}
		errs = append(errs, r.Errors...)
	}
	if msg == "" {
		msg = "MediaWiki API error"
	}
	return &MediaWikiApiError{
		Code:       code,
		Message:    msg,
		HTTPStatus: r.StatusCode,
		Errors:     errs,
		Response:   r,
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
