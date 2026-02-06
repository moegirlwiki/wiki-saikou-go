package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wiki-saikou/wiki-saikou-go/mwapi"
)

type envConfig struct {
	Endpoint string
	Username string
	Password string
}

func main() {
	ctx := context.Background()

	// Best-effort load .env from current working directory.
	// It will NOT override already-set environment variables.
	_ = loadDotEnv(".env")

	cfg, err := readConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	c, err := mwapi.NewClient(cfg.Endpoint, mwapi.WithThrowOnApiError(true))
	if err != nil {
		log.Fatal(err)
	}

	login, err := c.Login(ctx, cfg.Username, cfg.Password)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("login ok: %s (id=%d)", login.LgName, login.LgUserID)

	userInfo, err := queryUserInfo(ctx, c)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("userinfo: name=%s id=%d editcount=%d", userInfo.Name, userInfo.ID, userInfo.EditCount)

	title := fmt.Sprintf("User:%s/wiki-saikou-go", userInfo.Name)
	ts := time.Now().UTC().Format(time.RFC3339)
	text := buildDemoText(ts, cfg.Endpoint)

	editResp, err := c.PostWithToken(ctx, mwapi.TokenCSRF, map[string]any{
		"action":  "edit",
		"title":   title,
		"text":    text,
		"summary": fmt.Sprintf("demo update timestamp: %s", ts),
		"minor":   true,
	}, nil)
	if err != nil {
		log.Fatal(err)
	}

	edit, err := parseEditResult(editResp.Raw)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("edit ok: %s newrevid=%d timestamp=%s", edit.Title, edit.NewRevID, edit.NewTimestamp)
}

func readConfigFromEnv() (envConfig, error) {
	var cfg envConfig
	cfg.Endpoint = strings.TrimSpace(os.Getenv("MW_API_ENDPOINT"))
	cfg.Username = strings.TrimSpace(os.Getenv("MW_USERNAME"))
	cfg.Password = os.Getenv("MW_PASSWORD")

	var missing []string
	if cfg.Endpoint == "" {
		missing = append(missing, "MW_API_ENDPOINT")
	}
	if cfg.Username == "" {
		missing = append(missing, "MW_USERNAME")
	}
	if cfg.Password == "" {
		missing = append(missing, "MW_PASSWORD")
	}
	if len(missing) > 0 {
		return envConfig{}, fmt.Errorf("missing env: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

type userInfo struct {
	Name      string `json:"name"`
	ID        int    `json:"id"`
	EditCount int    `json:"editcount"`
}

func queryUserInfo(ctx context.Context, c *mwapi.Client) (*userInfo, error) {
	resp, err := c.Get(ctx, map[string]any{
		"action": "query",
		"meta":   "userinfo",
		"uiprop": []string{"editcount"},
	})
	if err != nil {
		return nil, err
	}

	var out struct {
		Query struct {
			UserInfo userInfo `json:"userinfo"`
		} `json:"query"`
	}
	if err := resp.Into(&out); err != nil {
		return nil, err
	}
	if out.Query.UserInfo.Name == "" {
		return nil, errors.New("missing userinfo.name in response")
	}
	return &out.Query.UserInfo, nil
}

type editResult struct {
	Result       string `json:"result"`
	Title        string `json:"title"`
	NewRevID     int64  `json:"newrevid"`
	NewTimestamp string `json:"newtimestamp"`
}

func parseEditResult(raw json.RawMessage) (*editResult, error) {
	var out struct {
		Edit editResult `json:"edit"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if strings.ToLower(out.Edit.Result) != "success" {
		if out.Edit.Result == "" {
			return nil, errors.New("missing edit.result in response")
		}
		return nil, fmt.Errorf("edit failed: %s", out.Edit.Result)
	}
	return &out.Edit, nil
}

func buildDemoText(ts string, endpoint string) string {
	return strings.TrimSpace(fmt.Sprintf(`
== wiki-saikou-go demo ==

Updated at: %s (UTC)

API endpoint: %s

This page is written by the demo program in [https://github.com/wiki-saikou/wiki-saikou-go wiki-saikou-go] for real-world testing.
`, ts, endpoint)) + "\n"
}

// loadDotEnv reads KEY=VALUE lines from a file and sets them into the process environment.
// It only sets variables that are not already present.
func loadDotEnv(path string) error {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		val := strings.TrimSpace(v)
		val = strings.TrimPrefix(val, "\"")
		val = strings.TrimSuffix(val, "\"")
		val = strings.TrimPrefix(val, "'")
		val = strings.TrimSuffix(val, "'")

		_ = os.Setenv(key, val)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}
