package mwapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/sync/singleflight"
)

type PostWithTokenOptions struct {
	TokenName string
	Retry     int
	NoCache   bool
}

func (c *Client) InvalidateToken(tokenType TokenType) {
	c.mu.Lock()
	delete(c.tokens, tokenType)
	c.mu.Unlock()
}

func (c *Client) InvalidateAllTokens() {
	c.mu.Lock()
	c.tokens = map[TokenType]string{}
	c.mu.Unlock()
}

func (c *Client) GetToken(ctx context.Context, tokenType TokenType) (string, error) {
	c.mu.Lock()
	if tok := c.tokens[tokenType]; tok != "" {
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	// Prevent token stampede within a single process.
	v, err, _ := c.tokenSF().Do("token:"+string(tokenType), func() (any, error) {
		c.mu.Lock()
		if tok := c.tokens[tokenType]; tok != "" {
			c.mu.Unlock()
			return tok, nil
		}
		c.mu.Unlock()

		resp, err := c.Post(ctx, map[string]any{
			"action": "query",
			"meta":   "tokens",
			"type":   string(tokenType),
		})
		if err != nil {
			return "", err
		}

		tok, err := extractToken(resp.Raw, tokenType)
		if err != nil {
			return "", err
		}

		c.mu.Lock()
		c.tokens[tokenType] = tok
		c.mu.Unlock()
		return tok, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (c *Client) PostWithToken(ctx context.Context, tokenType TokenType, p map[string]any, opt *PostWithTokenOptions) (*Response, error) {
	tokenName := "token"
	retry := c.tokenRetry
	noCache := false
	if opt != nil {
		if opt.TokenName != "" {
			tokenName = opt.TokenName
		}
		if opt.Retry > 0 {
			retry = opt.Retry
		}
		if opt.NoCache {
			noCache = true
		}
	}

	p2 := map[string]any{}
	for k, v := range p {
		p2[k] = v
	}

	var lastErr error
	for attempt := 0; attempt < retry; attempt++ {
		if attempt > 0 || noCache {
			c.InvalidateToken(tokenType)
		}

		tok, err := c.GetToken(ctx, tokenType)
		if err != nil {
			return nil, err
		}
		p2[tokenName] = tok

		resp, err := c.Post(ctx, p2)
		if err == nil {
			// Even when throwOnApiError=false, token errors can appear in envelope.
			if code := responseErrorCode(resp); isTokenErrorCode(code) {
				lastErr = &MediaWikiApiError{
					Code:       code,
					Message:    "token error",
					HTTPStatus: resp.StatusCode,
					Response:   resp,
				}
				continue
			}
			return resp, nil
		}

		lastErr = err
		if e, ok := IsMediaWikiApiError(err); ok && isTokenErrorCode(e.Code) {
			continue
		}
		return resp, err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("token retry exhausted")
	}
	return nil, fmt.Errorf("token retry exhausted: %w", lastErr)
}

func responseErrorCode(resp *Response) string {
	if resp == nil {
		return ""
	}
	if resp.Error != nil {
		return resp.Error.Code
	}
	if len(resp.Errors) > 0 {
		return resp.Errors[0].Code
	}
	return ""
}

func extractToken(raw json.RawMessage, tokenType TokenType) (string, error) {
	var r struct {
		Query struct {
			Tokens map[string]string `json:"tokens"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}

	key := strings.ToLower(string(tokenType)) + "token"
	tok := r.Query.Tokens[key]
	if tok == "" {
		return "", fmt.Errorf("missing %s token", tokenType)
	}
	return tok, nil
}

func (c *Client) tokenSF() *singleflight.Group {
	// singleflight.Group has no internal state that needs init; keep it in Client without pointer.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c._sf == nil {
		c._sf = &singleflight.Group{}
	}
	return c._sf
}
