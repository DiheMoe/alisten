package bootstrap

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dihemoe/alisten/internal/adapter/out/github"
)

// 假的 GitHub 有 client-1 和 client-2 两个 OAuth App，Client Secret 分别是 secret-1 和 secret-2。用户在授权页同意时签发授权码
// code-<n>，授权码绑定授权地址中的 client_id 和 code_challenge，换取令牌时的 client_id 和 code_verifier 必须和绑定的值对上。
// 授权码 code-<n> 换到令牌 token-<n>，令牌 token-<n> 是用户 n。
type fakeGitHub struct {
	t        *testing.T
	endpoint github.Endpoint
	mu       sync.Mutex
	approved map[string]approval
}

type approval struct {
	clientID  string
	challenge string
}

var githubSecrets = map[string]string{"client-1": "secret-1", "client-2": "secret-2"}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{t: t, approved: map[string]approval{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		clientID := r.FormValue("client_id")
		if r.FormValue("redirect_uri") != "https://alisten.example/api/auth/github/callback" {
			t.Errorf("换取令牌的参数 = %v", r.Form)
		}
		if secret, ok := githubSecrets[clientID]; !ok || r.FormValue("client_secret") != secret {
			_, _ = w.Write([]byte(`{"error":"incorrect_client_credentials","error_description":"The client_id and/or client_secret passed are incorrect."}`))
			return
		}
		code := r.FormValue("code")
		f.mu.Lock()
		a, approved := f.approved[code]
		delete(f.approved, code)
		f.mu.Unlock()
		verifier := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		n, ok := strings.CutPrefix(code, "code-")
		if !ok || !approved || a.clientID != clientID || base64.RawURLEncoding.EncodeToString(verifier[:]) != a.challenge {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token-" + n, "token_type": "bearer"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		n, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer token-")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","id":` + n + `}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	f.endpoint = github.Endpoint{AuthorizeURL: server.URL + "/login/oauth/authorize", TokenURL: server.URL + "/login/oauth/access_token", UserURL: server.URL + "/user"}
	return f
}

// 模拟用户在授权页同意一次授权，GitHub 签发授权码 code。authorize 是这次授权的授权地址的参数。
func (f *fakeGitHub) approve(authorize url.Values, code string) {
	f.t.Helper()
	if authorize.Get("code_challenge_method") != "S256" || authorize.Get("code_challenge") == "" {
		f.t.Errorf("授权地址的参数 = %s，期望带上 S256 的 code_challenge", authorize.Encode())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved[code] = approval{clientID: authorize.Get("client_id"), challenge: authorize.Get("code_challenge")}
}

type githubClient struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	github *fakeGitHub
}

func (c githubClient) call(method, path, token, body string) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.server.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	var v map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			c.t.Fatalf("%s %s 的响应体不是 JSON：%s", method, path, raw)
		}
	}
	return resp.StatusCode, v
}

func (c githubClient) guest() string {
	c.t.Helper()
	status, body := c.call(http.MethodPost, "/api/members", "", "{}")
	if status != http.StatusCreated {
		c.t.Fatalf("创建游客 = %d %v", status, body)
	}
	return body["memberToken"].(string)
}

// 发起授权，返回授权地址的参数。currentPassword 为空时请求中不带当前密码。
func (c githubClient) authorize(token, intent, nonce, currentPassword string) url.Values {
	c.t.Helper()
	return c.start(token, startGitHubBody(intent, nonce, currentPassword))
}

// 用请求体 request 发起授权，返回授权地址的参数。
func (c githubClient) start(token, request string) url.Values {
	c.t.Helper()
	status, body := c.call(http.MethodPost, "/api/auth/github", token, request)
	if status != http.StatusCreated {
		c.t.Fatalf("发起授权 = %d %v", status, body)
	}
	u, err := url.Parse(body["url"].(string))
	if err != nil {
		c.t.Fatal(err)
	}
	q := u.Query()
	if githubSecrets[q.Get("client_id")] == "" || q.Get("redirect_uri") != "https://alisten.example/api/auth/github/callback" || q.Get("state") == "" {
		c.t.Errorf("授权地址 = %s", u)
	}
	return q
}

// 在后台配置 OAuth App。
func (c githubClient) configure(clientID string) {
	c.t.Helper()
	body := `{"clientId":"` + clientID + `","clientSecret":"` + githubSecrets[clientID] + `"}`
	if status, resp := c.call(http.MethodPut, "/api/admin/credentials/github", "admin-secret", body); status != http.StatusNoContent {
		c.t.Fatalf("配置 GitHub OAuth App = %d %v", status, resp)
	}
}

// 回调地址的查询串里有授权码和 state，换取令牌的请求体和后台的请求体里有 Client Secret。授权码、state 和 Client Secret 都不能出现在 span 和日志中。
func checkNoGitHubSecrets(t *testing.T, svc service, values ...string) {
	t.Helper()
	values = append(values, "secret-1", "secret-2")
	for _, span := range svc.spans.Ended() {
		texts := []string{span.Status().Description}
		for _, kv := range span.Attributes() {
			texts = append(texts, kv.Value.String())
		}
		for _, event := range span.Events() {
			for _, kv := range event.Attributes {
				texts = append(texts, kv.Value.String())
			}
		}
		for _, text := range texts {
			for _, secret := range values {
				if strings.Contains(text, secret) {
					t.Errorf("span %s 中有 %q：%q", span.Name(), secret, text)
				}
			}
		}
	}
	logs := svc.logs.String()
	for _, secret := range values {
		if strings.Contains(logs, secret) {
			t.Errorf("日志中有 %q：\n%s", secret, logs)
		}
	}
}

// 模拟浏览器从 GitHub 跳回来，返回跳转到的地址。
func (c githubClient) callback(query string) string {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodGet, c.server.URL+"/api/auth/github/callback?"+query, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		c.t.Errorf("回调 = %d，Cache-Control %q，Referrer-Policy %q", resp.StatusCode, resp.Header.Get("Cache-Control"), resp.Header.Get("Referrer-Policy"))
	}
	return resp.Header.Get("Location")
}

func startGitHubBody(intent, nonce, currentPassword string) string {
	if currentPassword == "" {
		return `{"intent":"` + intent + `","nonce":"` + nonce + `"}`
	}
	return `{"intent":"` + intent + `","nonce":"` + nonce + `","currentPassword":"` + currentPassword + `"}`
}

// 走完授权和回调，返回兑换码。
func (c githubClient) exchangeCode(token, intent, nonce, currentPassword, code string) string {
	c.t.Helper()
	authorize := c.authorize(token, intent, nonce, currentPassword)
	c.github.approve(authorize, code)
	location := c.callback(url.Values{"code": {code}, "state": {authorize.Get("state")}}.Encode())
	exchange, ok := strings.CutPrefix(location, "/auth/github#code=")
	if !ok || exchange == "" {
		c.t.Fatalf("回调跳转到 %q，期望 /auth/github#code=<兑换码>", location)
	}
	return exchange
}

func (c githubClient) finish(token, exchange, nonce string) (int, map[string]any) {
	c.t.Helper()
	return c.call(http.MethodPost, "/api/auth/github/exchange", token, `{"code":"`+exchange+`","nonce":"`+nonce+`"}`)
}

func TestGitHubLogin(t *testing.T) {
	newDatabase, newRedis := startPostgres(t), startValkey(t)
	contractEnv(t, newDatabase(t), newRedis(t))
	fake := newFakeGitHub(t)
	svc := startService(t, func(cfg *serveConfig) { cfg.github.Endpoint = fake.endpoint })
	client := svc.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := githubClient{t: t, server: svc.server, client: client, github: fake}
	c.configure("client-1")
	const nonce = "nonce-0123456789"

	if status, body := c.call(http.MethodGet, "/api/site", "", ""); status != http.StatusOK || body["githubLogin"] != true {
		t.Errorf("配置了 GitHub 时站点 = %d %v，期望 githubLogin 为 true", status, body)
	}

	// 首次用 GitHub 登录就是注册，成员和成员 token 都不变。
	alice := c.guest()
	status, body := c.finish(alice, c.exchangeCode(alice, "login", nonce, "", "code-42"), nonce)
	if status != http.StatusOK || body["memberToken"] != nil || !equalJSON(body["account"], `{"github":true}`) {
		t.Fatalf("首次用 GitHub 登录 = %d %v，期望注册成只绑定了 GitHub 的账号", status, body)
	}
	aliceID := body["member"].(map[string]any)["id"]
	if status, body := c.call(http.MethodGet, "/api/me", alice, ""); status != http.StatusOK || !equalJSON(body["account"], `{"github":true}`) {
		t.Errorf("注册之后 /api/me = %d %v", status, body)
	}

	// 另一台设备用同一个 GitHub 登录，得到账号的新 token，游客的 token 随即失效。
	bob := c.guest()
	status, body = c.finish(bob, c.exchangeCode(bob, "login", nonce, "", "code-42"), nonce)
	if status != http.StatusOK || body["memberToken"] == nil || body["member"].(map[string]any)["id"] != aliceID {
		t.Fatalf("用已绑定的 GitHub 登录 = %d %v，期望登录同一个账号", status, body)
	}
	if status, _ := c.call(http.MethodGet, "/api/me", bob, ""); status != http.StatusUnauthorized {
		t.Errorf("登录之后游客原来的 token = %d，期望 401", status)
	}

	// 兑换码只能由发起授权的设备带着自己的 nonce 兑换。
	carol := c.guest()
	exchange := c.exchangeCode(carol, "login", nonce, "", "code-7")
	if status, body := c.finish(carol, exchange, "another-nonce-0000"); status != http.StatusUnauthorized || body["error"].(map[string]any)["code"] != "INVALID_CREDENTIALS" {
		t.Errorf("nonce 不对 = %d %v，期望 INVALID_CREDENTIALS", status, body)
	}

	// 回调失败时跳转到落地页并带上错误码。
	state := c.authorize(carol, "login", nonce, "").Get("state")
	if got := c.callback(url.Values{"code": {"wrong"}, "state": {state}}.Encode()); got != "/auth/github#error=failed" {
		t.Errorf("GitHub 不认这个授权码 = %q", got)
	}
	if got := c.callback(url.Values{"code": {"code-7"}, "state": {state}}.Encode()); got != "/auth/github#error=expired" {
		t.Errorf("同一个 state 再回调 = %q", got)
	}
	if got := c.callback(url.Values{"error": {"access_denied"}, "state": {state}}.Encode()); got != "/auth/github#error=denied" {
		t.Errorf("state 已经作废时用户在 GitHub 上取消 = %q", got)
	}

	// 授权码只能配合发起那次授权时的 verifier 换取令牌。所以把别人的授权码放进自己发起的回调里，换不到别人的身份。
	c.github.approve(c.authorize(carol, "login", nonce, ""), "code-7")
	mallory := c.guest()
	attack := c.authorize(mallory, "login", nonce, "").Get("state")
	if got := c.callback(url.Values{"code": {"code-7"}, "state": {attack}}.Encode()); got != "/auth/github#error=failed" {
		t.Errorf("用别人的授权码回调 = %q，期望 GitHub 不认", got)
	}
	if !slices.ContainsFunc(svc.spans.Ended(), func(span sdktrace.ReadOnlySpan) bool { return span.Name() == "GET /api/auth/github/callback" }) {
		t.Error("回调请求也应记 span")
	}
	checkNoGitHubSecrets(t, svc, attack, "code-7")

	// 没有密码的账号不能解绑。用 GitHub 确认身份、设置用户名和密码之后就可以解绑。
	if status, body := c.call(http.MethodPost, "/api/account/github/removals", alice, `{"currentPassword":"correct horse"}`); status != http.StatusConflict {
		t.Errorf("没有密码的账号解绑 = %d %v，期望 409", status, body)
	}
	if status, body := c.finish(alice, c.exchangeCode(alice, "verify", nonce, "", "code-42"), nonce); status != http.StatusOK {
		t.Fatalf("用 GitHub 确认身份 = %d %v", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account", alice, `{"username":"alice","password":"correct horse"}`); status != http.StatusCreated || !equalJSON(body["account"], `{"username":"alice","github":true}`) {
		t.Errorf("补设用户名和密码 = %d %v", status, body)
	}
	if status, _ := c.call(http.MethodPost, "/api/account/github/removals", alice, `{"currentPassword":"correct horse"}`); status != http.StatusNoContent {
		t.Errorf("设置密码之后解绑 = %d，期望 204", status)
	}

	// 已注册的账号绑定 GitHub。关闭注册不影响绑定，但没有绑定过的 GitHub 不能再注册。
	if status, _ := c.call(http.MethodDelete, "/api/admin/registration", "admin-secret", ""); status != http.StatusNoContent {
		t.Fatalf("关闭注册 = %d", status)
	}
	if status, body := c.finish(alice, c.exchangeCode(alice, "bind", nonce, "correct horse", "code-42"), nonce); status != http.StatusOK || body["memberToken"] != nil || !equalJSON(body["account"], `{"username":"alice","github":true}`) {
		t.Errorf("绑定 = %d %v", status, body)
	}
	if status, body := c.finish(carol, c.exchangeCode(carol, "login", nonce, "", "code-7"), nonce); status != http.StatusForbidden {
		t.Errorf("关闭注册时首次用 GitHub 登录 = %d %v，期望 403", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/auth/github", alice, startGitHubBody("bind", nonce, "correct horse")); status != http.StatusConflict {
		t.Errorf("已经绑定了 GitHub 的账号再发起绑定 = %d %v，期望 409", status, body)
	}
}

// App 发起授权时带上 client app，回调跳回 App 的自定义 scheme。跳转目标只看发起授权时记下的 client，回调地址里的参数改变不了跳转目标。
func TestGitHubAppCallback(t *testing.T) {
	newDatabase, newRedis := startPostgres(t), startValkey(t)
	contractEnv(t, newDatabase(t), newRedis(t))
	fake := newFakeGitHub(t)
	svc := startService(t, func(cfg *serveConfig) { cfg.github.Endpoint = fake.endpoint })
	client := svc.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := githubClient{t: t, server: svc.server, client: client, github: fake}
	c.configure("client-1")
	const (
		nonce   = "nonce-0123456789"
		app     = `{"intent":"login","nonce":"` + nonce + `","client":"app"}`
		web     = `{"intent":"login","nonce":"` + nonce + `","client":"web"}`
		omitted = `{"intent":"login","nonce":"` + nonce + `"}`
		landing = "moe.dihe.alisten:/auth/github#"
	)

	if status, body := c.call(http.MethodPost, "/api/auth/github", c.guest(), `{"intent":"login","nonce":"`+nonce+`","client":"ios"}`); status != http.StatusBadRequest {
		t.Errorf("不认识的 client = %d %v，期望 400", status, body)
	}

	alice := c.guest()
	authorize := c.start(alice, app)
	c.github.approve(authorize, "code-42")
	location := c.callback(url.Values{"code": {"code-42"}, "state": {authorize.Get("state")}}.Encode())
	exchange, ok := strings.CutPrefix(location, landing+"code=")
	if !ok || exchange == "" {
		t.Fatalf("App 发起的授权回调跳转到 %q，期望 %scode=<兑换码>", location, landing)
	}
	if status, body := c.finish(alice, exchange, nonce); status != http.StatusOK || !equalJSON(body["account"], `{"github":true}`) {
		t.Errorf("App 兑换 = %d %v，期望注册成只绑定了 GitHub 的账号", status, body)
	}

	// 用户在 GitHub 上拒绝时也跳回 App，state 随即作废。作废之后再回调就跳到网页。
	bob := c.guest()
	state := c.start(bob, app).Get("state")
	if got := c.callback(url.Values{"error": {"access_denied"}, "state": {state}}.Encode()); got != landing+"error=denied" {
		t.Errorf("用户在 GitHub 上拒绝 App 发起的授权 = %q", got)
	}
	if got := c.callback(url.Values{"code": {"code-7"}, "state": {state}}.Encode()); got != "/auth/github#error=expired" {
		t.Errorf("拒绝之后用同一个 state 回调 = %q，期望 state 已经作废，跳到网页", got)
	}
	if got := c.callback(url.Values{"error": {"access_denied"}, "state": {state}}.Encode()); got != "/auth/github#error=denied" {
		t.Errorf("state 已经作废时拒绝 = %q，期望跳到网页", got)
	}

	state = c.start(bob, app).Get("state")
	if got := c.callback(url.Values{"error": {"redirect_uri_mismatch"}, "state": {state}}.Encode()); got != landing+"error=failed" {
		t.Errorf("GitHub 拒绝 App 发起的授权请求 = %q", got)
	}
	state = c.start(bob, app).Get("state")
	if got := c.callback(url.Values{"code": {"wrong"}, "state": {state}}.Encode()); got != landing+"error=failed" {
		t.Errorf("GitHub 不认 App 发起的授权的授权码 = %q", got)
	}

	hijack := func(client string) string {
		return url.Values{
			"client": {client}, "redirect_uri": {"https://evil.example/"}, "redirect": {"https://evil.example/"},
			"redirect_to": {"evil://steal"}, "return_to": {"https://evil.example/"}, "next": {"//evil.example/"},
		}.Encode()
	}
	for _, tc := range []struct{ name, request, client, want string }{
		{"省略 client", omitted, "app", "/auth/github#code="},
		{"client 为 web", web, "app", "/auth/github#code="},
		{"client 为 app", app, "web", landing + "code="},
	} {
		authorize := c.start(c.guest(), tc.request)
		c.github.approve(authorize, "code-7")
		got := c.callback(url.Values{"code": {"code-7"}, "state": {authorize.Get("state")}}.Encode() + "&" + hijack(tc.client))
		if code, ok := strings.CutPrefix(got, tc.want); !ok || code == "" {
			t.Errorf("%s，回调地址中塞进跳转参数 = %q，期望 %s<兑换码>", tc.name, got, tc.want)
		}
	}
	state = c.start(c.guest(), omitted).Get("state")
	if got := c.callback(url.Values{"error": {"access_denied"}, "state": {state}}.Encode() + "&" + hijack("app")); got != "/auth/github#error=denied" {
		t.Errorf("网页发起的授权被拒绝，回调地址中塞进跳转参数 = %q，期望跳到网页", got)
	}
}

// 只凭会话不能更换登录凭证。绑定和解绑 GitHub 要输入当前密码。只绑定了 GitHub 的账号补设用户名和密码之前，要用账号绑定的
// GitHub 确认身份，补设之后其他设备全部退出。
func TestGitHubReauthentication(t *testing.T) {
	newDatabase, newRedis := startPostgres(t), startValkey(t)
	contractEnv(t, newDatabase(t), newRedis(t))
	fake := newFakeGitHub(t)
	svc := startService(t, func(cfg *serveConfig) { cfg.github.Endpoint = fake.endpoint })
	client := svc.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := githubClient{t: t, server: svc.server, client: client, github: fake}
	c.configure("client-1")
	const nonce = "nonce-0123456789"
	errorCode := func(body map[string]any) any {
		e, _ := body["error"].(map[string]any)
		return e["code"]
	}

	alice := c.guest()
	if status, body := c.call(http.MethodPost, "/api/account", alice, `{"username":"alice","password":"correct horse"}`); status != http.StatusCreated {
		t.Fatalf("注册 = %d %v", status, body)
	}
	for _, tc := range []struct{ name, password string }{{"不带当前密码", ""}, {"当前密码错误", "wrong horse"}} {
		if status, body := c.call(http.MethodPost, "/api/auth/github", alice, startGitHubBody("bind", nonce, tc.password)); status != http.StatusUnauthorized || errorCode(body) != "INVALID_CREDENTIALS" {
			t.Errorf("%s时发起绑定 = %d %v，期望 INVALID_CREDENTIALS", tc.name, status, body)
		}
	}
	if status, body := c.finish(alice, c.exchangeCode(alice, "bind", nonce, "correct horse", "code-42"), nonce); status != http.StatusOK || !equalJSON(body["account"], `{"username":"alice","github":true}`) {
		t.Fatalf("输入正确的当前密码之后绑定 = %d %v", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account/github/removals", alice, `{}`); status != http.StatusBadRequest || errorCode(body) != "INVALID_ARGUMENT" {
		t.Errorf("不带当前密码解绑 = %d %v，期望 INVALID_ARGUMENT", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account/github/removals", alice, `{"currentPassword":"wrong horse"}`); status != http.StatusUnauthorized || errorCode(body) != "INVALID_CREDENTIALS" {
		t.Errorf("当前密码错误时解绑 = %d %v，期望 INVALID_CREDENTIALS", status, body)
	}
	if status, body := c.call(http.MethodGet, "/api/me", alice, ""); status != http.StatusOK || !equalJSON(body["account"], `{"username":"alice","github":true}`) {
		t.Errorf("解绑失败之后 /api/me = %d %v，期望仍然绑定", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account/github/removals", alice, `{"currentPassword":"correct horse"}`); status != http.StatusNoContent {
		t.Errorf("输入正确的当前密码之后解绑 = %d %v，期望 204", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/auth/github", alice, startGitHubBody("verify", nonce, "")); status != http.StatusConflict {
		t.Errorf("设置了密码的账号发起确认 = %d %v，期望 409", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/auth/github", c.guest(), startGitHubBody("verify", nonce, "")); status != http.StatusForbidden {
		t.Errorf("游客发起确认 = %d %v，期望 403", status, body)
	}

	// 只绑定了 GitHub 的账号在两台设备上登录。
	octocat := c.guest()
	if status, body := c.finish(octocat, c.exchangeCode(octocat, "login", nonce, "", "code-7"), nonce); status != http.StatusOK {
		t.Fatalf("用 GitHub 注册 = %d %v", status, body)
	}
	other := c.guest()
	status, body := c.finish(other, c.exchangeCode(other, "login", nonce, "", "code-7"), nonce)
	if status != http.StatusOK || body["memberToken"] == nil {
		t.Fatalf("另一台设备用 GitHub 登录 = %d %v", status, body)
	}
	other = body["memberToken"].(string)

	const credentials = `{"username":"octocat","password":"correct horse"}`
	if status, body := c.call(http.MethodPost, "/api/account", octocat, credentials); status != http.StatusForbidden {
		t.Errorf("没有用 GitHub 确认就补设 = %d %v，期望 403", status, body)
	}
	if status, body := c.finish(octocat, c.exchangeCode(octocat, "verify", nonce, "", "code-42"), nonce); status != http.StatusUnauthorized || errorCode(body) != "INVALID_CREDENTIALS" {
		t.Errorf("用别的 GitHub 确认 = %d %v，期望 INVALID_CREDENTIALS", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account", octocat, credentials); status != http.StatusForbidden {
		t.Errorf("用别的 GitHub 确认之后补设 = %d %v，期望 403", status, body)
	}
	status, body = c.finish(octocat, c.exchangeCode(octocat, "verify", nonce, "", "code-7"), nonce)
	if status != http.StatusOK || body["memberToken"] != nil || !equalJSON(body["account"], `{"github":true}`) {
		t.Fatalf("用绑定的 GitHub 确认 = %d %v，期望成员 token 不变", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account", other, credentials); status != http.StatusForbidden {
		t.Errorf("另一台设备用这次确认补设 = %d %v，期望 403", status, body)
	}
	if status, body := c.call(http.MethodPost, "/api/account", octocat, credentials); status != http.StatusCreated || !equalJSON(body["account"], `{"username":"octocat","github":true}`) {
		t.Errorf("确认之后补设 = %d %v", status, body)
	}
	if status, _ := c.call(http.MethodGet, "/api/me", other, ""); status != http.StatusUnauthorized {
		t.Errorf("补设之后另一台设备的 token = %d，期望 401", status)
	}
	if status, _ := c.call(http.MethodGet, "/api/me", octocat, ""); status != http.StatusOK {
		t.Errorf("补设之后发出请求的 token = %d，期望 200", status)
	}
}

// 每次授权和兑换都读后台最新的配置，所有实例立即生效。授权进行中换了 OAuth App，GitHub 不认原来签发的授权码，回调失败。
// 关闭 GitHub 登录之后，GitHub 相关的接口返回 NOT_FOUND，回调跳到发起授权的客户端，带上 error=failed。
func TestGitHubOAuthAppChanges(t *testing.T) {
	newDatabase, newRedis := startPostgres(t), startValkey(t)
	contractEnv(t, newDatabase(t), newRedis(t))
	fake := newFakeGitHub(t)
	endpoint := func(cfg *serveConfig) { cfg.github.Endpoint = fake.endpoint }
	first, second := startService(t, endpoint), startService(t, endpoint)
	clientOf := func(svc service) githubClient {
		client := svc.server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return githubClient{t: t, server: svc.server, client: client, github: fake}
	}
	a, b := clientOf(first), clientOf(second)
	const (
		nonce    = "nonce-0123456789"
		app      = `{"intent":"login","nonce":"` + nonce + `","client":"app"}`
		disabled = `{"code":"NOT_FOUND","message":"站点没有开启 GitHub 登录"}`
	)
	a.configure("client-1")

	alice := b.guest()
	stale := b.authorize(alice, "login", nonce, "")
	b.github.approve(stale, "code-42")
	a.configure("client-2")
	if got := b.authorize(alice, "login", nonce, "").Get("client_id"); got != "client-2" {
		t.Errorf("另一个实例上换了 OAuth App 之后，授权地址的 client_id = %q，期望 client-2", got)
	}
	if got := b.callback(url.Values{"code": {"code-42"}, "state": {stale.Get("state")}}.Encode()); got != "/auth/github#error=failed" {
		t.Errorf("授权进行中换了 OAuth App，回调 = %q，期望 GitHub 不认原来签发的授权码", got)
	}
	if status, body := b.finish(alice, b.exchangeCode(alice, "login", nonce, "", "code-42"), nonce); status != http.StatusOK || !equalJSON(body["account"], `{"github":true}`) {
		t.Fatalf("换了 OAuth App 之后用 GitHub 注册 = %d %v", status, body)
	}

	bob := b.guest()
	exchange := b.exchangeCode(bob, "login", nonce, "", "code-7")
	pending := b.start(bob, app)
	b.github.approve(pending, "code-7")
	if status, body := a.call(http.MethodDelete, "/api/admin/credentials/github", "admin-secret", ""); status != http.StatusNoContent {
		t.Fatalf("关闭 GitHub 登录 = %d %v", status, body)
	}
	if status, body := b.call(http.MethodGet, "/api/site", "", ""); status != http.StatusOK || body["githubLogin"] != false {
		t.Errorf("关闭之后另一个实例上的站点 = %d %v，期望 githubLogin 为 false", status, body)
	}
	if status, body := b.finish(bob, exchange, nonce); status != http.StatusNotFound || !equalJSON(body["error"], disabled) {
		t.Errorf("关闭之后兑换 = %d %v，期望 NOT_FOUND", status, body)
	}
	if got := b.callback(url.Values{"code": {"code-7"}, "state": {pending.Get("state")}}.Encode()); got != "moe.dihe.alisten:/auth/github#error=failed" {
		t.Errorf("关闭之后 App 发起的授权回调 = %q，期望跳回 App 并带上 error=failed", got)
	}
	if got := b.callback(url.Values{"code": {"code-7"}, "state": {"forged"}}.Encode()); got != "/auth/github#error=failed" {
		t.Errorf("关闭之后不存在的 state 回调 = %q，期望 error=failed：重新发起也没有用，不提示过期", got)
	}
	if status, body := b.call(http.MethodPost, "/api/auth/github", bob, startGitHubBody("login", nonce, "")); status != http.StatusNotFound || !equalJSON(body["error"], disabled) {
		t.Errorf("关闭之后发起授权 = %d %v，期望 NOT_FOUND", status, body)
	}
	if status, body := b.call(http.MethodPost, "/api/account/github/removals", alice, `{"currentPassword":"correct horse"}`); status != http.StatusNotFound || !equalJSON(body["error"], disabled) {
		t.Errorf("关闭之后解绑 = %d %v，期望 NOT_FOUND", status, body)
	}
	if status, body := b.call(http.MethodGet, "/api/me", bob, ""); status != http.StatusOK || body["account"] != nil {
		t.Errorf("关闭之后兑换失败的游客 = %d %v，期望没有注册账号", status, body)
	}
	checkNoGitHubSecrets(t, first)
	checkNoGitHubSecrets(t, second)
}

func equalJSON(got any, want string) bool {
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		panic(err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(w)
	return string(a) == string(b)
}
