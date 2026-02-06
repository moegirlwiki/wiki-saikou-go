package mwapi

import (
	"errors"
	"fmt"
	"strings"
)

type MediaWikiApiError struct {
	Code       string
	Message    string
	HTTPStatus int
	Errors     []MWError
	Response   *Response
}

func (e *MediaWikiApiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func IsMediaWikiApiError(err error) (*MediaWikiApiError, bool) {
	var e *MediaWikiApiError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func isTokenErrorCode(code string) bool {
	switch strings.ToLower(code) {
	case "badtoken", "notoken", "needtoken", "wrongtoken":
		return true
	default:
		return false
	}
}

func isAssertUserFailedCode(code string) bool {
	switch strings.ToLower(code) {
	case "assertuserfailed", "assertnameduserfailed":
		return true
	default:
		return false
	}
}
