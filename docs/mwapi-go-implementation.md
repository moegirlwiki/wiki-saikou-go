# mwapi-go 实现文档（草案）

目标：实现一个 Go 版 MediaWiki API 客户端 SDK，支持：

- `New(endpoint)` 创建客户端
- `Login(user, pass)` 登录并维持会话（cookies）
- 自动维护 tokens（至少 CSRF token），并支持 `PostWithToken(...)`
- 暂不实现 `/rest.php` 对应功能（仅 Action API `/api.php`）

核心成功标准：

- 登录后跨请求保持会话（cookie jar 生效）
- token 获取与缓存正确；遇到 token 相关错误（如 `badtoken` / `NeedToken` / `WrongToken`）能自动刷新 token 并按配置重试（建议默认最多尝试 `3` 次）
- 调用方式尽量贴近 `mw.Api` 的“先 new，再 login（可选），然后可以 get/post/postWithToken”

---

## 背景与关键约束

- MediaWiki Action API 默认是 `application/x-www-form-urlencoded` POST（也支持 GET）。
  - 当请求体包含 File 时，需要转而使用 `multipart/form-data` 请求。
- Token 通常从 `action=query&meta=tokens` 获取；token 与会话强绑定，会话变更/过期会导致 token 失效；只要没有切换用户或者收到 `badtoken` 错误，同一 token 可以在一段时间内重复使用。
- 错误格式通常在 JSON 里包含 `error.code/error.info`，但 warning/continue 等字段也要保留。
- 本 Go 版客户端面向服务端/脚本环境，不处理浏览器跨域相关语义：
  - 不自动注入/迁移/编码 `origin` 参数
  - 不承诺对齐浏览器端 CORS 行为（如必须将 `origin` 放在 query 的约束）

---

## 代码结构建议（按现有仓库风格拆分）

建议创建 Go module（例如 `mwapi-go/` 或直接在 repo 下建 `go.mod`，看你项目规划）。包名建议 `mwapi`。

文件划分（建议）：

- `mwapi/client.go`：Client 结构、New/Option、底层 doRequest、Post/Get
- `mwapi/params.go`：参数归一化（map/url.Values/struct）
- `mwapi/auth.go`：Login 实现（clientlogin/login）
- `mwapi/tokens.go`：token 获取/缓存/失效逻辑
- `mwapi/errors.go`：MW error -> Go error
- `mwapi/types.go`：通用 response envelope（error/warnings/continue）

---

## API 设计（尽量贴近 mw.Api，但 Go 化）

JS 风格：

```js
api := mwApi.New('https://example.com/api.php')
api.Login('user', 'passwd')
api.PostWithToken(body)
```

Go 建议接口（推荐）：

```go
c := mwapi.New("https://example.com/api.php")

login, err := c.Login(ctx, "user", "passwd")
if err != nil { ... }
_ = login // contains lguserid/lgusername

resp, err := c.PostWithToken(ctx, mwapi.TokenCSRF, map[string]string{
  "action": "edit",
  "title":  "Sandbox",
  "text":   "hello",
})
```

关键点：

- Go 需要 `context.Context`（超时/取消），否则库不好用
- `PostWithToken` 需要明确 token 类型（最常用 CSRF），避免“隐式猜测”
- 返回值建议给“原始 JSON + 解码能力”两层：
  - 高层：用户传入 struct 指针 `Into(&out)` 解码
  - 低层：保留 `map[string]any`/`json.RawMessage` 以兼容各种 action

---

## HTTP 与 Cookie 维护（会话基础）

实现要点：

- `http.Client` 必须配置 `cookiejar.Jar` 才能自动保存/携带 cookies
- 允许通过 Option 注入自定义 transport（代理、TLS、重试等）
- 默认 UA 建议包含库名/版本，便于 wiki 侧排查

伪代码：

```go
type Client struct {
  endpoint *url.URL
  hc       *http.Client
  ua       string

  mu      sync.Mutex
  tokens  map[string]string // tokenType -> token
}

func New(endpoint string, opts ...Option) *Client {
  u := mustParse(endpoint)

  jar, _ := cookiejar.New(nil)
  hc := &http.Client{ Jar: jar, Timeout: 30 * time.Second }

  c := &Client{
    endpoint: u,
    hc: hc,
    ua: "mwapi-go/0.1",
    tokens: map[string]string{},
  }
  applyOptions(c, opts)
  return c
}
```

注意事项：

- endpoint 需要规范化（确保是 `.../api.php`，并处理已有 query）
- 不要在日志里输出敏感字段：`password`、`token`、`lgpassword` 等

---

## 参数编码与默认参数

建议内部统一用 `url.Values`（或“等价的表单键值集合”），并明确默认参数与归一化规则。

与 TypeScript 版 `wiki-saikou` 对齐的默认参数建议：

- 默认 `action=query`
- 默认 `format=json`
- 默认 `formatversion=2`
- 默认 `errorformat=plaintext`

> 说明：`errorformat=plaintext` 会让错误信息更稳定、便于直接展示；`formatversion=2` 会让响应结构更统一（但仍需兼容少量非典型字段，如 `info`/`*`）。

- 输入可以接受 `map[string]string` / `url.Values`
- 内部统一注入上述默认参数（允许调用方显式覆盖）
- 对数组参数（如 `prop=revisions|info`）使用 MediaWiki 约定的 `|` 拼接，或允许用户传字符串

参数值归一化（建议与 TS 版一致）：

- `[]string` / `[]any`：使用 `|` 连接（例如 `['a','b'] -> 'a|b'`）
- `bool`：
  - `true`：发送 `'1'`
  - `false`：不发送该字段（等价于未设置）
- `nil`：不发送该字段
- `number`：转字符串
- 文件/二进制（`io.Reader`/`[]byte`/`os.File` 等）：触发 `multipart/form-data`，否则使用 `application/x-www-form-urlencoded`

关于 GET vs POST 的差异建议：

- GET：使用 query string（`url.Values.Encode()`）
- POST：
  - 无文件：`application/x-www-form-urlencoded`
  - 有文件：`multipart/form-data`

另外，建议在实现层面处理“重复参数”的优先级（与 TS 版一致的经验值）：

- 若某些 key 同时出现在 query 与 body，则 **body 优先**（避免 query 与 body 冲突导致行为不确定）

伪代码：

```go
func normalizeParams(p any) url.Values {
  v := url.Values{}
  // map[string]string -> v.Set(k, val)
  // url.Values -> copy
  v.Set("action", "query")
  v.Set("format", "json")
  v.Set("formatversion", "2")
  v.Set("errorformat", "plaintext")
  return v
}

func (c *Client) Post(ctx context.Context, p any) (*Response, error) {
  v := normalizeParams(p)
  req, _ := http.NewRequestWithContext(ctx, "POST", c.endpoint.String(), strings.NewReader(v.Encode()))
  req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
  req.Header.Set("User-Agent", c.ua)

  // do + decode
}
```

---

## 通用响应 Envelope 与错误处理

建议定义一个“最小 envelope”覆盖所有请求，并显式对齐 TS 版的两类错误来源：

- **API 业务错误**：HTTP 成功，但 JSON 内含 `error` 或 `errors[]`
- **网络/SDK 错误**：HTTP/transport 失败、SDK 内部重试耗尽、配置错误等

并提供一个开关（类似 TS 版的 `throwOnApiError`）来决定默认行为：

- `throwOnApiError=false`（默认）：不把 `error/errors[]` 转为 Go error，调用方自行检查（更贴近 `mw.Api` 体验）
- `throwOnApiError=true`：将 `error/errors[]` 映射为 `*mwapi.MediaWikiApiError`（即使 HTTP status=2xx）

```go
type Envelope struct {
  Error    *MWError           `json:"error,omitempty"`
  Errors   []MWError          `json:"errors,omitempty"`
  Warnings map[string]any     `json:"warnings,omitempty"`
  Continue map[string]string  `json:"continue,omitempty"`
  // Query/Edit/... 不在这里固定，保留 Raw
  Raw json.RawMessage `json:"-"`
}
type MWError struct {
  Code string `json:"code"`
  // MediaWiki 常见字段：formatversion=2 通常是 info/text；formatversion=1 可能出现 "*"
  Info string `json:"info,omitempty"`
  Text string `json:"text,omitempty"`
}
```

处理原则：

- 在 `throwOnApiError=true` 时，如果 JSON 包含 `error` 或 `errors[]`，返回一个实现 `error` 的类型（如 `*mwapi.MediaWikiApiError`），包含：
  - Code（首个 error code，或对 badtoken 做特殊归类）
  - Message（拼接 error.text/info）
  - 可选携带 HTTP status、部分原始 body（注意脱敏）
- 把 `badtoken`、以及登录返回的 `NeedToken/WrongToken` 等状态视为“token 相关错误”，供 token 层自动恢复
- `warnings/continue/limits` 等字段不一定失败，但必须可被调用方读取（例如 `Response.Warnings()` / `Response.Continue()`）

---

## Token 获取、缓存与自动失效

核心接口：

- `GetToken(ctx, tokenType)`：从缓存取；没有则请求获取并缓存
- `InvalidateToken(tokenType)` / `InvalidateAllTokens()`：显式清缓存
- `PostWithToken(ctx, tokenType, params, options...)`：注入 token，并对“token 相关错误”自动重试

建议对齐 TS 版的 `PostWithToken` options（最少集）：

- `tokenName`：token 字段名（默认 `token`；login 对应 `lgtoken`；clientlogin 对应 `logintoken`）
- `retry`：自动重试次数（建议默认 `3`）
- `noCache`：是否跳过 token 缓存（建议默认 `false`；但 login 通常建议 `true`）

token API 请求（典型）：

- `action=query&meta=tokens&type=csrf`
- 响应在 `query.tokens.csrftoken`

伪代码：

```go
func (c *Client) GetToken(ctx context.Context, tokenType string) (string, error) {
  c.mu.Lock()
  if tok := c.tokens[tokenType]; tok != "" {
    c.mu.Unlock()
    return tok, nil
  }
  c.mu.Unlock()

  resp, err := c.Post(ctx, map[string]string{
    "action": "query",
    "meta":   "tokens",
    "type":   tokenType,
  })
  if err != nil { return "", err }

  tok := extractToken(resp.Raw, tokenType) // parse query.tokens.<...>
  if tok == "" { return "", fmt.Errorf("missing %s token", tokenType) }

  c.mu.Lock()
  c.tokens[tokenType] = tok
  c.mu.Unlock()
  return tok, nil
}

type PostWithTokenOptions struct {
  TokenName string
  Retry     int
  NoCache   bool
}

func (c *Client) PostWithToken(ctx context.Context, tokenType string, p map[string]string, opt *PostWithTokenOptions) (*Response, error) {
  tokenName := "token"
  retry := 3
  noCache := false
  if opt != nil {
    if opt.TokenName != "" { tokenName = opt.TokenName }
    if opt.Retry > 0 { retry = opt.Retry }
    if opt.NoCache { noCache = true }
  }

  tok, err := c.GetToken(ctx, tokenType)
  if err != nil { return nil, err }

  p2 := copyMap(p)
  p2[tokenName] = tok

  // 对 token 相关错误自动重试（badtoken / NeedToken / WrongToken 等）
  var lastErr error
  for attempt := 0; attempt < retry; attempt++ {
    if attempt > 0 {
      // attempt>0 时强制刷新 token（与 noCache 语义合并）
      c.InvalidateToken(tokenType)
      tok2, err2 := c.GetToken(ctx, tokenType)
      if err2 != nil { return nil, err2 }
      p2[tokenName] = tok2
    } else if noCache {
      c.InvalidateToken(tokenType)
      tok2, err2 := c.GetToken(ctx, tokenType)
      if err2 != nil { return nil, err2 }
      p2[tokenName] = tok2
    }

    resp, err := c.Post(ctx, p2)
    if err == nil {
      if isBadTokenResponse(resp) {
        lastErr = newBadTokenError(resp)
        continue
      }
      return resp, nil
    }
    if isBadToken(err) {
      lastErr = err
      continue
    }
    return nil, err
  }
  return nil, fmt.Errorf("token retry exhausted: %w", lastErr)
}
```

并发注意事项：

- `tokens` map 必须加锁
- 如果你希望高并发下避免“击穿”重复取 token，可选 `singleflight.Group`（可后置优化）

---

## Login 实现

字段名 `lgname/lgpassword/lgtoken`

伪代码（抽象化）：

```go
type LoginResult struct {
  Result   string `json:"result"`
  LgUserID int    `json:"lguserid"`
  LgName   string `json:"lgusername"`
}

func (c *Client) Login(ctx context.Context, user, pass string) (*LoginResult, error) {
  // 建议：login token 默认 noCache=true（与 TS 版一致），避免复用旧 token 导致 WrongToken/NeedToken
  resp, err := c.PostWithToken(ctx, mwapi.TokenLogin, map[string]string{
    "action":     "login",
    "lgname":     user,
    "lgpassword": pass,
  }, &PostWithTokenOptions{TokenName: "lgtoken", NoCache: true})
  if err != nil { return nil, err }

  r := extractLoginResult(resp.Raw)
  if r == nil || r.Result != "Success" {
    return nil, fmt.Errorf("login failed: %s", safeLoginReason(resp.Raw))
  }

  // 登录态变化后，所有 token 都可能失效
  c.InvalidateAllTokens()
  // 可选：记录用户名以启用 keep login（assertuser）
  c.setLoggedInUser(r.LgName)
  return r, nil
}
```

登录成功验证：

- api 返回了 `login.result` 为 `Success` 则登录成功，并且通常可以得到 username 与 userid

---

## 维持登录（Session 续航）

cookie jar 会自动带上 session cookie；库需要处理的是：

一旦用户登录成功过（并启用 keep login），则为接下来的请求自动添加 `assertuser=username` 参数，并在掉线后自动重登+重放请求。

与 TS 版一致的关键语义建议：

- keep login 默认开启（可配置关闭）
- 自动注入 `assertuser` 时，需要跳过这些请求，避免递归/破坏登录流程：
  - `action=login`
  - `action=query&meta=tokens&type=login`（获取 login token 的请求）
- 如果遇到 `assertuserfailed` / `assertnameduserfailed`：
  - 使用保存的账号密码自动重新登录
  - 重新发送原请求
  - 该流程有最大重试次数（建议默认 `3`，允许配置为 `0` 来禁用重放）

建议：

- 暴露 `IsLoggedIn(ctx)`（可选）
- 增加 `Relogin(ctx)` 方法，用于在 session 过期后重新登录
- 增加 `Logout(ctx)` 方法，用于登出

---

## 示例（对外文档可直接放 README/Docs）

```go
c := mwapi.New("https://example.com/w/api.php",
  mwapi.WithUserAgent("mybot/1.0 (contact@example.com)"),
)

if err := c.Login(ctx, "user", "passwd"); err != nil {
  log.Fatal(err)
}

_, err := c.PostWithToken(ctx, mwapi.TokenCSRF, map[string]string{
  "action": "edit",
  "title":  "Sandbox",
  "text":   "hello",
  "summary":"edit via mwapi-go",
})
if err != nil { log.Fatal(err) }
```

---

## 风险与回滚策略

- 风险：token 并发击穿/错误缓存
  - 缓解：互斥锁保护 + `badtoken` 自动刷新；必要时 singleflight
- 风险：日志泄露敏感信息
  - 缓解：统一 redaction（password/token 字段）再输出

回滚：

- 早期只提供 `Post/Get/Login/GetToken/PostWithToken` 五个核心接口；高级封装（Edit/Upload）后置，避免 API 面过早冻结。
