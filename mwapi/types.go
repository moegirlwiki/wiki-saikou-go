package mwapi

import (
	"encoding/json"
	"net/http"
)

type TokenType string

const (
	TokenCSRF  TokenType = "csrf"
	TokenLogin TokenType = "login"
)

type MWError struct {
	Code string `json:"code"`
	Info string `json:"info,omitempty"`
	Text string `json:"text,omitempty"`
}

type Envelope struct {
	Error    *MWError          `json:"error,omitempty"`
	Errors   []MWError         `json:"errors,omitempty"`
	Warnings map[string]any    `json:"warnings,omitempty"`
	Continue map[string]string `json:"continue,omitempty"`
}

type Response struct {
	StatusCode int
	Header     http.Header
	Envelope

	Raw json.RawMessage
}

func (r *Response) Into(out any) error {
	return json.Unmarshal(r.Raw, out)
}
