// Package mwapi provides a minimal MediaWiki Action API client.
//
// It focuses on server-side usage: cookie-based sessions, token management (CSRF/login),
// and a mw.Api-like workflow (New -> Login (optional) -> Get/Post/PostWithToken).
package mwapi
