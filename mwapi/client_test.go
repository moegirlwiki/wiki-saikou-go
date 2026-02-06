package mwapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostWithToken_RetryOnBadToken(t *testing.T) {
	t.Parallel()

	var tokenCalls atomic.Int32
	var editCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}

		switch r.Form.Get("action") {
		case "query":
			if r.Form.Get("meta") == "tokens" && r.Form.Get("type") == "csrf" {
				n := tokenCalls.Add(1)
				tok := "CSRF_1"
				if n >= 2 {
					tok = "CSRF_2"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"query": map[string]any{
						"tokens": map[string]any{
							"csrftoken": tok,
						},
					},
				})
				return
			}
		case "edit":
			editCalls.Add(1)
			if r.Form.Get("token") != "CSRF_2" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code": "badtoken",
						"info": "bad token",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"edit": map[string]any{"result": "Success"},
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code": "badtest",
				"info": "unhandled request",
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL + "/api.php")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	resp, err := c.PostWithToken(ctx, TokenCSRF, map[string]any{
		"action": "edit",
		"title":  "Sandbox",
		"text":   "hello",
	}, nil)
	if err != nil {
		t.Fatalf("PostWithToken: %v", err)
	}
	if resp == nil {
		t.Fatalf("resp is nil")
	}
	if got := tokenCalls.Load(); got != 2 {
		t.Fatalf("token calls = %d, want 2", got)
	}
	if got := editCalls.Load(); got != 2 {
		t.Fatalf("edit calls = %d, want 2", got)
	}
}

func TestKeepLogin_ReloginOnAssertUserFailed(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	var queryCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_ = r.ParseMultipartForm(32 << 20)
		} else {
			_ = r.ParseForm()
		}

		action := r.Form.Get("action")
		if action == "query" && r.Form.Get("meta") == "tokens" && r.Form.Get("type") == "login" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query": map[string]any{
					"tokens": map[string]any{
						"logintoken": "LOGIN_TOKEN",
					},
				},
			})
			return
		}
		if action == "login" {
			loginCalls.Add(1)
			if r.Form.Get("lgtoken") != "LOGIN_TOKEN" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"login": map[string]any{
						"result": "WrongToken",
					},
				})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "1", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login": map[string]any{
					"result":     "Success",
					"lguserid":   1,
					"lgusername": "UserA",
				},
			})
			return
		}

		if action == "query" && r.Form.Get("meta") == "" {
			n := queryCalls.Add(1)
			if got := r.Form.Get("assertuser"); got != "UserA" {
				t.Fatalf("assertuser=%q, want %q", got, "UserA")
			}
			if n == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code": "assertuserfailed",
						"info": "session expired",
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query": map[string]any{
					"normalized": []any{},
				},
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code": "badtest",
				"info": "unhandled request",
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL + "/api.php")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	if _, err := c.Login(ctx, "UserA", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, err := c.Post(ctx, map[string]any{
		"action": "query",
		"titles": "Main Page",
	}); err != nil {
		t.Fatalf("Post(query): %v", err)
	}

	if got := loginCalls.Load(); got != 2 {
		t.Fatalf("login calls = %d, want 2 (relogin should happen)", got)
	}
	if got := queryCalls.Load(); got != 2 {
		t.Fatalf("query calls = %d, want 2 (replay should happen)", got)
	}
}

func TestCookieJar_PersistsAfterLogin(t *testing.T) {
	t.Parallel()

	var sawCookieOnCSRFTokens atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		action := r.Form.Get("action")
		if action == "query" && r.Form.Get("meta") == "tokens" && r.Form.Get("type") == "login" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query": map[string]any{
					"tokens": map[string]any{
						"logintoken": "LOGIN_TOKEN",
					},
				},
			})
			return
		}
		if action == "login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "1", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login": map[string]any{
					"result":     "Success",
					"lguserid":   1,
					"lgusername": "UserA",
				},
			})
			return
		}
		if action == "query" && r.Form.Get("meta") == "tokens" && r.Form.Get("type") == "csrf" {
			if strings.Contains(r.Header.Get("Cookie"), "session=1") {
				sawCookieOnCSRFTokens.Store(true)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query": map[string]any{
					"tokens": map[string]any{
						"csrftoken": "CSRF_TOKEN",
					},
				},
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code": "badtest",
				"info": "unhandled request",
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL + "/api.php")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	if _, err := c.Login(ctx, "UserA", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, err := c.GetToken(ctx, TokenCSRF); err != nil {
		t.Fatalf("GetToken(CSRF): %v", err)
	}

	if !sawCookieOnCSRFTokens.Load() {
		t.Fatalf("expected session cookie to be sent after login")
	}
}
