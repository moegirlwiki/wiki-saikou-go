# Wiki Saikou Go

Go library for interacting with MediaWiki APIs (WIP).

## Requirements

- Go 1.22+

## Install

```bash
go get github.com/wiki-saikou/wiki-saikou-go
```

## Usage

This repo currently provides the `mwapi` package.

```go
package main

import (
	"context"
	"log"

	"github.com/wiki-saikou/wiki-saikou-go/mwapi"
)

func main() {
	ctx := context.Background()

	// Create a client (endpoint is the api.php URL)
	c, err := mwapi.NewClient("https://www.mediawiki.org/w/api.php")
	if err != nil {
		log.Fatal(err)
	}

	// --- GET example: query siteinfo ---
	{
		resp, err := c.Get(ctx, map[string]any{
			"action": "query",
			"meta":   "siteinfo",
			"siprop": []string{"general"},
		})
		if err != nil {
			log.Fatal(err)
		}

		var out struct {
			Query map[string]any `json:"query"`
		}
		if err := resp.Into(&out); err != nil {
			log.Fatal(err)
		}
		log.Println("siteinfo keys:", len(out.Query))
	}

	// --- POST example: query via POST (same params, different HTTP method) ---
	{
		resp, err := c.Post(ctx, map[string]any{
			"action": "query",
			"titles": []string{"Main Page", "Sandbox"},
			"prop":   []string{"info"},
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Println("post query status:", resp.StatusCode)
	}

	// --- PostWithToken example: server-side actions requiring CSRF token ---
	// This requires a logged-in session to be meaningful.
	{
		// Optional: login first
		// _, _ = c.Login(ctx, "Username", "Password")

		// Example A (safe): logout (no page modifications)
		_, _ = c.PostWithToken(ctx, mwapi.TokenCSRF, map[string]any{
			"action": "logout",
		}, nil)

		// Example B (template): edit (commented out to avoid accidental writes)
		// _, _ = c.PostWithToken(ctx, mwapi.TokenCSRF, map[string]any{
		// 	"action": "edit",
		// 	"title":  "Project:Sandbox",
		// 	"text":   "Hello from wiki-saikou-go",
		// }, nil)
	}
}
```

## Demo (login + userinfo + edit)

For real-world testing, there is a runnable demo at `demo/` which:

- reads `MW_API_ENDPOINT`, `MW_USERNAME`, `MW_PASSWORD` from `.env`
- logs in
- queries `meta=userinfo` to get the actual logged-in username
- writes current timestamp to `User:<username>/wiki-saikou-go`

### Run

Create `.env` in the repo root (it is ignored by git). You can start from `demo/.env.example`.

```bash
cp demo/.env.example .env
go run ./demo
```

If you want to build a binary, specify `-o` (because `demo/` directory already exists):

```bash
go build -o demo-bin ./demo
./demo-bin
```

## Development

This project uses a small `Makefile` wrapper:

```bash
make tidy   # go mod tidy
make fmt    # gofmt -w .
make vet    # go vet ./...
make test   # go test ./...
make lint   # golangci-lint run ./... (if installed)
make check  # fmt + vet + test
```

---

> MIT License
>
> Copyright (c) 2026 dragon-fish
