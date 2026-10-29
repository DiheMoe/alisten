package qq

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/lib/chinaip"
)

const (
	qqLoginAppID            = "716027609"
	qqLoginOAuthClient      = "100497308"
	qqLoginDaid             = "383"
	qqLoginXUIURL           = "https://xui.ptlogin2.qq.com/cgi-bin/xlogin"
	qqLoginQRShowURL        = "https://xui.ptlogin2.qq.com/ssl/ptqrshow"
	qqLoginQRCheckURL       = "https://xui.ptlogin2.qq.com/ssl/ptqrlogin"
	qqLoginOAuthJumpURL     = "https://graph.qq.com/oauth2.0/login_jump"
	qqLoginAuthorizeURL     = "https://graph.qq.com/oauth2.0/authorize"
	qqLoginMusicURL         = "https://u.y.qq.com/cgi-bin/musicu.fcg"
	qqLoginRedirectURI      = "https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https%3A%2F%2Fy.qq.com%2F"
	weChatLoginAppID        = "wx48db31d50e334801"
	weChatLoginRedirectURI  = "https://y.qq.com/portal/wx_redirect.html?login_type=2&surl=https://y.qq.com/"
	weChatLoginQRConnectURL = "https://open.weixin.qq.com/connect/qrconnect"
	weChatLoginQRCheckURL   = "https://lp.open.weixin.qq.com/connect/l/qrconnect"
	weChatLoginQRCodeURL    = "https://open.weixin.qq.com/connect/qrcode/"
)

var qqLoginMusicURLs = []string{
	qqLoginMusicURL,
	"https://szu.y.qq.com/cgi-bin/musicu.fcg",
	"https://shu.y.qq.com/cgi-bin/musicu.fcg",
}

var (
	_                    port.QQQRLoginSource = (*Client)(nil)
	weChatQRUUIDPattern                       = regexp.MustCompile(`(?:connect/l/qrconnect\?uuid=|window\.QRLogin\.uuid\s*=\s*["']|/connect/qrcode/)([A-Za-z0-9_-]+)`)
	weChatQRErrorPattern                      = regexp.MustCompile(`wx_errcode\s*=\s*([0-9]+)`)
	weChatQRCodePattern                       = regexp.MustCompile(`wx_code\s*=\s*["']([^"']*)["']`)
)

type qqQRLoginState struct {
	QRSig    string            `json:"qrsig"`
	LoginSig string            `json:"login_sig"`
	Cookies  map[string]string `json:"cookies"`
}

type weChatQRLoginState struct {
	UUID  string `json:"uuid"`
	State string `json:"state"`
}

func (c *Client) CreateQRLogin(ctx context.Context, loginType port.QQLoginType) (session port.QRLoginSession, err error) {
	switch loginType {
	case port.QQLoginQQ, port.QQLoginWeChat:
	default:
		return port.QRLoginSession{}, fmt.Errorf("未知的 QQ 登录类型 %q", loginType)
	}
	start := time.Now()
	defer func() { c.observe("qq", "login_create", time.Since(start), err) }()
	if loginType == port.QQLoginWeChat {
		return c.createWeChatQR(ctx)
	}
	return c.createQQQR(ctx)
}

func (c *Client) CheckQRLogin(ctx context.Context, loginType port.QQLoginType, key string) (result port.QRLoginResult, err error) {
	switch loginType {
	case port.QQLoginQQ, port.QQLoginWeChat:
	default:
		return port.QRLoginResult{}, fmt.Errorf("未知的 QQ 登录类型 %q", loginType)
	}
	if key == "" {
		return port.QRLoginResult{}, errors.New("QQ 扫码登录的 key 为空")
	}
	start := time.Now()
	defer func() { c.observe("qq", "login_check", time.Since(start), err) }()
	if loginType == port.QQLoginWeChat {
		return c.checkWeChatQR(ctx, key)
	}
	return c.checkQQQR(ctx, key)
}

func (c *Client) createQQQR(ctx context.Context) (port.QRLoginSession, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	client := qqLoginClient(c.api, jar)
	prewarm := url.Values{
		"appid":            {qqLoginAppID},
		"daid":             {qqLoginDaid},
		"style":            {"33"},
		"login_text":       {"登录"},
		"hide_title_bar":   {"1"},
		"hide_border":      {"1"},
		"target":           {"self"},
		"s_url":            {qqLoginOAuthJumpURL},
		"pt_3rd_aid":       {qqLoginOAuthClient},
		"pt_feedback_link": {"https://support.qq.com/products/77942?customInfo=.appid100497308"},
		"theme":            {"2"},
		"verify_theme":     {""},
	}
	resp, err := qqLoginRequest(ctx, client, http.MethodGet, qqLoginXUIURL+"?"+prewarm.Encode(), nil, nil)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	if _, err := readQQLoginBody(resp); err != nil {
		return port.QRLoginSession{}, err
	}
	qrQuery := url.Values{
		"appid":      {qqLoginAppID},
		"e":          {"2"},
		"l":          {"M"},
		"s":          {"3"},
		"d":          {"72"},
		"v":          {"4"},
		"t":          {strconv.FormatInt(time.Now().Unix(), 10)},
		"daid":       {qqLoginDaid},
		"pt_3rd_aid": {qqLoginOAuthClient},
		"u1":         {qqLoginOAuthJumpURL},
	}
	resp, err = qqLoginRequest(ctx, client, http.MethodGet, qqLoginQRShowURL+"?"+qrQuery.Encode(), nil, nil)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	image, err := readQQLoginBody(resp)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	state := qqQRLoginState{Cookies: make(map[string]string)}
	mergeQQLoginJar(state.Cookies, jar, qqLoginXUIURL, qqLoginQRShowURL, qqLoginQRCheckURL)
	for _, cookie := range resp.Cookies() {
		if cookie.Value != "" {
			state.Cookies[cookie.Name] = cookie.Value
		}
	}
	state.QRSig = state.Cookies["qrsig"]
	state.LoginSig = state.Cookies["pt_login_sig"]
	if state.QRSig == "" {
		return port.QRLoginSession{}, errors.New("QQ 创建扫码登录的响应缺少 qrsig")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	return port.QRLoginSession{
		Key:       base64.RawURLEncoding.EncodeToString(encoded),
		QRImage:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(image),
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}, nil
}

func (c *Client) checkQQQR(ctx context.Context, key string) (port.QRLoginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	encoded, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解码 QQ 扫码登录的 key：%w", err)
	}
	var state qqQRLoginState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析 QQ 扫码登录的 key：%w", err)
	}
	if state.QRSig == "" {
		return port.QRLoginResult{}, errors.New("QQ 扫码登录的 key 缺少 qrsig")
	}
	if state.Cookies == nil {
		state.Cookies = make(map[string]string)
	}
	state.Cookies["qrsig"] = state.QRSig
	if state.LoginSig != "" {
		state.Cookies["pt_login_sig"] = state.LoginSig
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	names := make([]string, 0, len(state.Cookies))
	for name := range state.Cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	cookies := make([]*http.Cookie, 0, len(names))
	for _, name := range names {
		if state.Cookies[name] != "" {
			cookies = append(cookies, &http.Cookie{Name: name, Value: state.Cookies[name], Path: "/"})
		}
	}
	for _, rawURL := range []string{qqLoginXUIURL, qqLoginQRShowURL, qqLoginQRCheckURL} {
		u, err := url.Parse(rawURL)
		if err != nil {
			return port.QRLoginResult{}, err
		}
		jar.SetCookies(u, cookies)
	}
	client := qqLoginClient(c.api, jar)
	query := url.Values{
		"u1":            {qqLoginOAuthJumpURL},
		"ptqrtoken":     {qqLoginHash(state.QRSig, 0)},
		"ptredirect":    {"0"},
		"h":             {"1"},
		"t":             {"1"},
		"g":             {"1"},
		"from_ui":       {"1"},
		"ptlang":        {"2052"},
		"action":        {"0-0-" + strconv.FormatInt(time.Now().UnixMilli(), 10)},
		"js_ver":        {"26071711"},
		"js_type":       {"1"},
		"login_sig":     {state.LoginSig},
		"pt_uistyle":    {"40"},
		"aid":           {qqLoginAppID},
		"daid":          {qqLoginDaid},
		"pt_3rd_aid":    {qqLoginOAuthClient},
		"pt_js_version": {"c1987b96"},
	}
	resp, err := qqLoginRequest(ctx, client, http.MethodGet, qqLoginQRCheckURL+"?"+query.Encode(), nil, nil)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	body, err := readQQLoginBody(resp)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	fields, err := qqLoginCallbackFields(string(body))
	if err != nil || len(fields) < 5 {
		return port.QRLoginResult{}, errors.New("解析 QQ 扫码轮询的响应失败")
	}
	code, err := strconv.Atoi(fields[0])
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析 QQ 扫码状态：%w", err)
	}
	result := port.QRLoginResult{Status: qqQRLoginStatus(code), Message: fields[4]}
	if result.Status != port.QRLoginSuccess {
		return result, nil
	}
	if fields[2] == "" {
		return port.QRLoginResult{}, errors.New("QQ 扫码成功的响应缺少跳转地址")
	}
	cookie, err := c.completeQQLogin(ctx, client, fields[2])
	if err != nil {
		return port.QRLoginResult{}, err
	}
	if cookie == "" {
		result.Status = port.QRLoginFailed
		result.Message = "QQ 音乐登录未返回音乐 key"
		return result, nil
	}
	if err := c.cookie.put(ctx, cookie); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("保存 QQ Cookie：%w", err)
	}
	return result, nil
}

func (c *Client) createWeChatQR(ctx context.Context) (port.QRLoginSession, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	state, err := qqLoginUUID()
	if err != nil {
		return port.QRLoginSession{}, err
	}
	query := url.Values{
		"appid":         {weChatLoginAppID},
		"redirect_uri":  {weChatLoginRedirectURI},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {state},
		"href":          {"https://y.qq.com/mediastyle/music_v17/src/css/popup_wechat.css#wechat_redirect"},
	}
	loginURL := weChatLoginQRConnectURL + "?" + query.Encode()
	client := qqLoginClient(c.api, nil)
	resp, err := qqLoginRequest(ctx, client, http.MethodGet, loginURL, nil, nil)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	html, err := readQQLoginBody(resp)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	matches := weChatQRUUIDPattern.FindSubmatch(html)
	if len(matches) != 2 || len(matches[1]) == 0 {
		return port.QRLoginSession{}, errors.New("微信创建扫码登录的响应缺少 UUID")
	}
	uuid := string(matches[1])
	encoded, err := json.Marshal(weChatQRLoginState{UUID: uuid, State: state})
	if err != nil {
		return port.QRLoginSession{}, err
	}
	return port.QRLoginSession{
		Key:       base64.RawURLEncoding.EncodeToString(encoded),
		LoginURL:  loginURL,
		QRImage:   weChatLoginQRCodeURL + url.PathEscape(uuid),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}, nil
}

func (c *Client) checkWeChatQR(ctx context.Context, key string) (port.QRLoginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	encoded, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解码微信扫码登录的 key：%w", err)
	}
	var state weChatQRLoginState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析微信扫码登录的 key：%w", err)
	}
	if state.UUID == "" {
		return port.QRLoginResult{}, errors.New("微信扫码登录的 key 缺少 UUID")
	}
	client := qqLoginClient(c.api, nil)
	query := url.Values{
		"uuid": {state.UUID},
		"_":    {strconv.FormatInt(time.Now().UnixMilli(), 10)},
	}
	resp, err := qqLoginRequest(ctx, client, http.MethodGet, weChatLoginQRCheckURL+"?"+query.Encode(), nil, nil)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	body, err := readQQLoginBody(resp)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	matches := weChatQRErrorPattern.FindSubmatch(body)
	if len(matches) != 2 {
		return port.QRLoginResult{}, errors.New("解析微信扫码轮询的响应失败")
	}
	code, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析微信扫码状态：%w", err)
	}
	result := port.QRLoginResult{Status: weChatQRLoginStatus(code)}
	if result.Status != port.QRLoginSuccess {
		return result, nil
	}
	matches = weChatQRCodePattern.FindSubmatch(body)
	if len(matches) != 2 || len(matches[1]) == 0 {
		return port.QRLoginResult{}, errors.New("微信扫码成功的响应缺少授权 code")
	}
	cookie, err := c.completeWeChatLogin(ctx, client, string(matches[1]))
	if err != nil {
		return port.QRLoginResult{}, err
	}
	if cookie == "" {
		result.Status = port.QRLoginFailed
		result.Message = "QQ 音乐登录未返回音乐 key"
		return result, nil
	}
	if err := c.cookie.put(ctx, cookie); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("保存 QQ Cookie：%w", err)
	}
	return result, nil
}

func weChatQRLoginStatus(code int) port.QRLoginStatus {
	switch code {
	case 405:
		return port.QRLoginSuccess
	case 402:
		return port.QRLoginExpired
	case 404:
		return port.QRLoginScanned
	case 408:
		return port.QRLoginWaiting
	default:
		return port.QRLoginFailed
	}
}

func (c *Client) completeWeChatLogin(ctx context.Context, client *http.Client, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	requestBody, err := json.Marshal(map[string]any{
		"comm": map[string]any{
			"tmeAppID": "qqmusic", "tmeLoginType": "1", "g_tk": 5381,
			"platform": "yqq", "ct": 24, "cv": 0,
		},
		"req": map[string]any{
			"module": "music.login.LoginServer",
			"method": "Login",
			"param":  map[string]string{"strAppid": weChatLoginAppID, "code": code},
		},
	})
	if err != nil {
		return "", err
	}
	header := http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
		"Referer":      {weChatLoginRedirectURI},
		"Origin":       {"https://y.qq.com"},
		"Cookie":       {"login_type=2"},
	}
	var errs []error
	for _, endpoint := range qqLoginMusicURLs {
		cookie, err := completeWeChatLoginAt(ctx, client, endpoint, requestBody, header)
		if err == nil {
			return cookie, nil
		}
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}

func completeWeChatLoginAt(ctx context.Context, client *http.Client, endpoint string, requestBody []byte, header http.Header) (string, error) {
	resp, err := qqLoginRequest(ctx, client, http.MethodPost, endpoint, bytes.NewReader(requestBody), header)
	if err != nil {
		return "", err
	}
	musicBody, err := readQQLoginBody(resp)
	if err != nil {
		return "", err
	}
	credentials := make(map[string]string)
	if err := mergeQQMusicLoginResponse(credentials, resp, musicBody); err != nil {
		return "", err
	}
	return serializeQQLoginCredentials(credentials), nil
}

func (c *Client) completeQQLogin(ctx context.Context, client *http.Client, redirectURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	finalJump, err := followQQLogin(ctx, client, redirectURL)
	if err != nil {
		return "", err
	}
	credentials := make(map[string]string)
	mergeQQLoginJar(credentials, client.Jar, finalJump, qqLoginAuthorizeURL, qqLoginRedirectURI)
	pSKey := credentials["p_skey"]
	if pSKey == "" {
		return "", errors.New("QQ 登录跳转后缺少 p_skey")
	}
	ui, err := qqLoginUUID()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"response_type": {"code"},
		"client_id":     {qqLoginOAuthClient},
		"redirect_uri":  {qqLoginRedirectURI},
		"scope":         {"get_user_info,get_app_friends"},
		"state":         {"state"},
		"switch":        {""},
		"from_ptlogin":  {"1"},
		"src":           {"1"},
		"update_auth":   {"1"},
		"openapi":       {"1010_1030"},
		"g_tk":          {qqLoginHash(pSKey, 5381)},
		"auth_time":     {strconv.FormatInt(time.Now().UnixMilli(), 10)},
		"ui":            {ui},
	}
	header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	resp, err := qqLoginRequest(ctx, client, http.MethodPost, qqLoginAuthorizeURL, strings.NewReader(form.Encode()), header)
	if err != nil {
		return "", err
	}
	finalOAuth := resp.Request.URL.String()
	if qqLoginRedirect(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if location == "" {
			closeQQLoginBody(resp)
			return "", errors.New("QQ OAuth 跳转缺少 Location")
		}
		next, err := resp.Request.URL.Parse(location)
		closeQQLoginBody(resp)
		if err != nil {
			return "", errors.New("解析 QQ OAuth 跳转地址失败")
		}
		finalOAuth, err = followQQLogin(ctx, client, next.String())
		if err != nil {
			return "", err
		}
	} else if _, err := readQQLoginBody(resp); err != nil {
		return "", err
	}
	mergeQQLoginJar(credentials, client.Jar, finalOAuth, qqLoginRedirectURI, qqLoginMusicURL)
	oauthURL, err := url.Parse(finalOAuth)
	if err != nil {
		return "", errors.New("解析 QQ OAuth 回调地址失败")
	}
	code := oauthURL.Query().Get("code")
	if code == "" {
		return "", errors.New("QQ OAuth 回调缺少 code")
	}
	requestBody, err := json.Marshal(map[string]any{
		"comm": map[string]any{"g_tk": 5381, "platform": "yqq", "ct": 24, "cv": 0},
		"req": map[string]any{
			"module": "QQConnectLogin.LoginServer",
			"method": "QQLogin",
			"param":  map[string]string{"code": code},
		},
	})
	if err != nil {
		return "", err
	}
	header = http.Header{
		"Content-Type": {"application/json"},
		"Referer":      {"https://y.qq.com/"},
		"Origin":       {"https://y.qq.com"},
	}
	resp, err = qqLoginRequest(ctx, client, http.MethodPost, qqLoginMusicURL, bytes.NewReader(requestBody), header)
	if err != nil {
		return "", err
	}
	musicBody, err := readQQLoginBody(resp)
	if err != nil {
		return "", err
	}
	mergeQQLoginJar(credentials, client.Jar, qqLoginMusicURL, finalOAuth, finalJump)
	if err := mergeQQMusicLoginResponse(credentials, resp, musicBody); err != nil {
		return "", err
	}
	return serializeQQLoginCredentials(credentials), nil
}

func mergeQQMusicLoginResponse(credentials map[string]string, resp *http.Response, musicBody []byte) error {
	var payload struct {
		Code *int `json:"code"`
		Req  struct {
			Code *int           `json:"code"`
			Data map[string]any `json:"data"`
		} `json:"req"`
	}
	decoder := json.NewDecoder(bytes.NewReader(musicBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("解析 QQ 音乐登录的响应：%w", err)
	}
	if payload.Code == nil || payload.Req.Code == nil {
		return errors.New("QQ 音乐登录响应的 code 或 req.code 缺失或为 null")
	}
	if *payload.Code != 0 || *payload.Req.Code != 0 {
		return fmt.Errorf("QQ 音乐登录返回错误码 %d、%d", *payload.Code, *payload.Req.Code)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Value != "" {
			credentials[cookie.Name] = cookie.Value
		}
	}
	for name, value := range payload.Req.Data {
		musicKey := false
		switch name {
		case "qm_keyst", "qqmusic_key", "musickey", "music_key", "strMusicKey":
			musicKey = true
		case "uin", "musicid", "userid", "user_id", "ptui_loginuin", "luin", "pt2gguin", "superuin", "p_uin", "wxuin",
			"refresh_key", "refresh_token", "openid", "unionid", "wxaccess_token":
		default:
			continue
		}
		credential := ""
		switch value := value.(type) {
		case string:
			if value == "" {
				continue
			}
			credential = value
		case json.Number:
			credential = value.String()
		default:
			continue
		}
		cookie := &http.Cookie{Name: name, Value: credential}
		if cookie.Valid() != nil {
			if musicKey {
				return fmt.Errorf("QQ 音乐登录响应的音乐 key 字段 %s 无效", name)
			}
			continue
		}
		credentials[name] = credential
	}
	return nil
}

func serializeQQLoginCredentials(credentials map[string]string) string {
	if !normalizeQQLoginCredentials(credentials) {
		return ""
	}
	names := make([]string, 0, len(credentials))
	for name, value := range credentials {
		if name != "" && value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	pairs := make([]string, len(names))
	for i, name := range names {
		pairs[i] = name + "=" + credentials[name]
	}
	return strings.Join(pairs, "; ")
}

func followQQLogin(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	manual := *client
	manual.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &manual
	current := rawURL
	for redirects := 0; ; {
		if !validQQLoginURL(current) {
			return "", errors.New("QQ 登录跳转地址仅允许 HTTPS 和 QQ 域名")
		}
		resp, err := qqLoginRequest(ctx, client, http.MethodGet, current, nil, nil)
		if err != nil {
			return "", err
		}
		if !qqLoginRedirect(resp.StatusCode) {
			finalURL := resp.Request.URL.String()
			if _, err := readQQLoginBody(resp); err != nil {
				return "", err
			}
			return finalURL, nil
		}
		if redirects >= 10 {
			closeQQLoginBody(resp)
			return "", errors.New("QQ 登录跳转超过 10 次")
		}
		location := resp.Header.Get("Location")
		if location == "" {
			closeQQLoginBody(resp)
			return "", errors.New("QQ 登录跳转缺少 Location")
		}
		next, err := resp.Request.URL.Parse(location)
		closeQQLoginBody(resp)
		if err != nil {
			return "", errors.New("解析 QQ 登录跳转地址失败")
		}
		current = next.String()
		redirects++
	}
}

func validQQLoginURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	return host == "qq.com" || strings.HasSuffix(host, ".qq.com")
}

func qqLoginClient(api *http.Client, jar http.CookieJar) *http.Client {
	client := *api
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func qqLoginRequest(ctx context.Context, client *http.Client, method, target string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if header != nil {
		req.Header = header.Clone()
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", desktopHeader.Get("User-Agent"))
	}
	chinaip.Set(req.Header)
	resp, err := client.Do(req)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		// net/http 会在 CheckRedirect 前解析 Location，这个错误没有独立类型。
		// 按文本识别并替换解析错误，避免完整登录跳转地址及查询参数进入错误或日志。
		if strings.Contains(err.Error(), "failed to parse Location header") {
			return nil, fmt.Errorf("请求 %s：解析 QQ 登录跳转地址失败", req.URL.Host)
		}
		return nil, fmt.Errorf("请求 %s：%w", req.URL.Host, err)
	}
	return resp, nil
}

func readQQLoginBody(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 HTTP %d", resp.Request.URL.Host, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 的响应：%w", resp.Request.URL.Host, err)
	}
	return body, nil
}

func closeQQLoginBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func qqLoginRedirect(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther ||
		status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

func qqLoginCallbackFields(body string) ([]string, error) {
	var fields []string
	var field strings.Builder
	quoted, escaped := false, false
	for _, r := range body {
		if !quoted {
			if r == '\'' {
				quoted = true
				field.Reset()
			}
			continue
		}
		if escaped {
			field.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '\'':
			fields = append(fields, field.String())
			quoted = false
		default:
			field.WriteRune(r)
		}
	}
	if quoted {
		return nil, errors.New("未闭合的回调字段")
	}
	return fields, nil
}

func qqQRLoginStatus(code int) port.QRLoginStatus {
	switch code {
	case 0:
		return port.QRLoginSuccess
	case 65:
		return port.QRLoginExpired
	case 66:
		return port.QRLoginWaiting
	case 67:
		return port.QRLoginScanned
	default:
		return port.QRLoginFailed
	}
}

func qqLoginHash(value string, initial int64) string {
	hash := initial
	for _, r := range value {
		hash += (hash << 5) + int64(r)
	}
	return strconv.FormatInt(hash&0x7fffffff, 10)
}

func qqLoginUUID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}

func mergeQQLoginJar(credentials map[string]string, jar http.CookieJar, rawURLs ...string) {
	for _, rawURL := range rawURLs {
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		for _, cookie := range jar.Cookies(u) {
			if cookie.Value != "" {
				credentials[cookie.Name] = cookie.Value
			}
		}
	}
}

func normalizeQQLoginCredentials(credentials map[string]string) bool {
	musicKey := ""
	for _, name := range []string{"qm_keyst", "qqmusic_key", "musickey", "music_key", "strMusicKey"} {
		if credentials[name] != "" {
			musicKey = credentials[name]
			break
		}
	}
	if musicKey == "" {
		return false
	}
	if credentials["qm_keyst"] == "" {
		credentials["qm_keyst"] = musicKey
	}
	if credentials["qqmusic_key"] == "" {
		credentials["qqmusic_key"] = musicKey
	}
	if credentials["uin"] == "" {
		for _, name := range []string{"musicid", "userid", "user_id", "ptui_loginuin", "luin", "pt2gguin", "superuin", "p_uin", "wxuin"} {
			if credentials[name] != "" {
				credentials["uin"] = credentials[name]
				break
			}
		}
	}
	return true
}
