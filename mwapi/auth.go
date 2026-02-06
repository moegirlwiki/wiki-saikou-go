package mwapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type LoginResult struct {
	Result   string `json:"result"`
	LgUserID int    `json:"lguserid"`
	LgName   string `json:"lgusername"`
	Reason   string `json:"reason,omitempty"`
}

func (c *Client) Login(ctx context.Context, user, pass string) (*LoginResult, error) {
	retry := c.tokenRetry
	var lastErr error

	for attempt := 0; attempt < retry; attempt++ {
		// Login token is sensitive to session state; avoid reusing cached one.
		c.InvalidateToken(TokenLogin)
		tok, err := c.GetToken(ctx, TokenLogin)
		if err != nil {
			return nil, err
		}

		resp, err := c.Post(ctx, map[string]any{
			"action":     "login",
			"lgname":     user,
			"lgpassword": pass,
			"lgtoken":    tok,
		})
		if err != nil {
			lastErr = err
			if e, ok := IsMediaWikiApiError(err); ok && isTokenErrorCode(e.Code) {
				continue
			}
			return nil, err
		}

		var out struct {
			Login LoginResult `json:"login"`
		}
		if err := json.Unmarshal(resp.Raw, &out); err != nil {
			return nil, err
		}

		switch strings.ToLower(out.Login.Result) {
		case "success":
			c.mu.Lock()
			c.loginUser = user
			c.loginPass = pass
			c.loggedInUser = out.Login.LgName
			c.mu.Unlock()

			// Session changed; invalidate all tokens.
			c.InvalidateAllTokens()
			return &out.Login, nil
		case "needtoken", "wrongtoken":
			lastErr = fmt.Errorf("login token error: %s", out.Login.Result)
			continue
		default:
			if out.Login.Reason != "" {
				return &out.Login, fmt.Errorf("login failed: %s (%s)", out.Login.Result, out.Login.Reason)
			}
			return &out.Login, fmt.Errorf("login failed: %s", out.Login.Result)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("login retry exhausted")
	}
	return nil, fmt.Errorf("login retry exhausted: %w", lastErr)
}

func (c *Client) Relogin(ctx context.Context) error {
	c.mu.Lock()
	user := c.loginUser
	pass := c.loginPass
	c.mu.Unlock()

	if user == "" || pass == "" {
		return fmt.Errorf("relogin requested but no stored credentials")
	}
	_, err := c.Login(ctx, user, pass)
	return err
}

func (c *Client) Logout(ctx context.Context) error {
	_, err := c.PostWithToken(ctx, TokenCSRF, map[string]any{
		"action": "logout",
	}, nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.loggedInUser = ""
	c.loginUser = ""
	c.loginPass = ""
	c.mu.Unlock()
	c.InvalidateAllTokens()
	return nil
}
