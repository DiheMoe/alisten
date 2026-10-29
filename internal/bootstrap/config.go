package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/adapter/out/storage"
)

type serveConfig struct {
	listen         string
	opsListen      string
	publicOrigin   string
	allowedOrigins []string
	adminToken     string
	database       *pgxpool.Config
	redis          *goredis.Options
	storage        storage.Config
	// 浏览器访问对象存储时使用的地址前缀，结尾没有 /。
	storageURL string
	tracing    bool
}

func loadServeConfig() (serveConfig, error) {
	listen, err := listenAddr("ALISTEN_LISTEN", ":8080")
	if err != nil {
		return serveConfig{}, err
	}
	opsListen, err := listenAddr("ALISTEN_OPS_LISTEN", ":9090")
	if err != nil {
		return serveConfig{}, err
	}
	publicURL := os.Getenv("ALISTEN_PUBLIC_URL")
	// 示例配置在 ALISTEN_DOMAIN 为空时生成 https://，它与空值一样使用本地默认地址。
	if publicURL == "" || publicURL == "https://" {
		publicURL = "http://localhost:9876"
	}
	origin, err := publicOrigin(publicURL)
	if err != nil {
		return serveConfig{}, err
	}
	allowedOrigins, err := parseAllowedOrigins(os.Getenv("ALISTEN_ALLOWED_ORIGINS"), origin)
	if err != nil {
		return serveConfig{}, err
	}
	adminToken := os.Getenv("ALISTEN_ADMIN_TOKEN")
	database, err := loadDatabaseConfig()
	if err != nil {
		return serveConfig{}, err
	}
	redis, err := loadRedisConfig()
	if err != nil {
		return serveConfig{}, err
	}
	objects, err := loadStorageConfig()
	if err != nil {
		return serveConfig{}, err
	}
	storagePublicURL := os.Getenv("STORAGE_PUBLIC_URL")
	if storagePublicURL == "" && strings.HasPrefix(origin, "http://") {
		storagePublicURL = origin + "/storage/" + objects.Bucket
	}
	storageURL, err := parseStorageURL(storagePublicURL)
	if err != nil {
		return serveConfig{}, err
	}
	tracing, err := loadTracing()
	if err != nil {
		return serveConfig{}, err
	}
	return serveConfig{
		listen:         listen,
		opsListen:      opsListen,
		publicOrigin:   origin,
		allowedOrigins: allowedOrigins,
		adminToken:     adminToken,
		database:       database,
		redis:          redis,
		storage:        objects,
		storageURL:     storageURL,
		tracing:        tracing,
	}, nil
}

// 浏览器发送的 Origin 中，scheme 和主机名都是小写，默认端口省略不写，端口不带前导零，国际化域名写成 punycode，
// IP 地址写成规范形式。浏览器不会发出的写法在启动时就报错：否则带 Origin 的请求全部被拒绝。
func publicOrigin(value string) (string, error) {
	if value == "" {
		return "", errors.New("缺少 ALISTEN_PUBLIC_URL")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("ALISTEN_PUBLIC_URL 的值 %q 不是 http 或 https 的绝对地址", value)
	}
	if port := u.Port(); strings.HasSuffix(u.Host, ":") || port != "" && !canonicalPort(port) {
		return "", fmt.Errorf("ALISTEN_PUBLIC_URL 的值 %q 中的端口不合法，必须是 1–65535 的整数，不带前导零", value)
	}
	if strings.ContainsFunc(u.Hostname(), func(r rune) bool { return r >= utf8.RuneSelf }) {
		return "", fmt.Errorf("ALISTEN_PUBLIC_URL 的值 %q 中的主机名要写成 punycode", value)
	}
	if !canonicalHost(strings.ToLower(u.Hostname()), strings.HasPrefix(u.Host, "[")) {
		return "", fmt.Errorf("ALISTEN_PUBLIC_URL 的值 %q 中的 IP 地址要写成浏览器发送的规范形式", value)
	}
	defaultPort := map[string]string{"http": ":80", "https": ":443"}[u.Scheme]
	return u.Scheme + "://" + strings.TrimSuffix(strings.ToLower(u.Host), defaultPort), nil
}

func parseAllowedOrigins(value, fallback string) ([]string, error) {
	if value == "" {
		return []string{fallback}, nil
	}
	var origins []string
	for _, raw := range strings.Split(value, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			return nil, errors.New("ALISTEN_ALLOWED_ORIGINS 中有空地址")
		}
		origin, err := publicOrigin(item)
		if err != nil {
			return nil, fmt.Errorf("ALISTEN_ALLOWED_ORIGINS 中的 %q：%w", item, err)
		}
		if !slices.Contains(origins, origin) {
			origins = append(origins, origin)
		}
	}
	return origins, nil
}

func canonicalPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port
}

var numericLabel = regexp.MustCompile(`^(?:[0-9]+|0[xX][0-9a-fA-F]*)$`)

// 方括号中的主机是 IPv6 地址：浏览器把它写成小写并压缩连续的零，不带 zone，IPv4 映射地址也写成十六进制。
// 最后一个标签是数字的主机名，浏览器按 IPv4 解析（127.1、0x7f000001、010.0.0.1 都能解析）之后写成点分十进制，
// 所以只有 netip 接受的写法（四段十进制，没有前导零）才与浏览器发送的一致。其他主机名不是 IP 地址，原样发送。
func canonicalHost(host string, bracketed bool) bool {
	if bracketed {
		addr, err := netip.ParseAddr(host)
		return err == nil && addr.Zone() == "" && !addr.Is4In6() && addr.String() == host
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if !numericLabel.MatchString(labels[len(labels)-1]) {
		return true
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

// 链接曲目的音频和封面地址由这个前缀直接拼接对象键而成，所以不能带查询串和片段，哪怕只有一个 ? 或 #。
// 这个地址会发给每个浏览器，所以也不能带用户信息。
func parseStorageURL(value string) (string, error) {
	if err := checkHTTPURL("STORAGE_PUBLIC_URL", value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(value, "/"), nil
}

// bucket 名称会写进访问策略和对象的地址，所以只接受 S3 规定的字符。
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func loadStorageConfig() (storage.Config, error) {
	endpoint := os.Getenv("STORAGE_ENDPOINT_URL")
	if err := checkHTTPURL("STORAGE_ENDPOINT_URL", endpoint); err != nil {
		return storage.Config{}, err
	}
	region := envOr("STORAGE_REGION", "us-east-1")
	if region == "" {
		return storage.Config{}, errors.New("STORAGE_REGION 不能为空")
	}
	bucket := envOr("STORAGE_BUCKET_NAME", "alisten")
	if !bucketPattern.MatchString(bucket) {
		return storage.Config{}, fmt.Errorf("STORAGE_BUCKET_NAME 的值 %q 不是合法的 bucket 名称：只能由小写字母、数字、点和连字符组成，长度 3–63", bucket)
	}
	cfg := storage.Config{
		Endpoint:        endpoint,
		Region:          region,
		Bucket:          bucket,
		AccessKeyID:     os.Getenv("STORAGE_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("STORAGE_SECRET_ACCESS_KEY"),
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return storage.Config{}, errors.New("缺少 STORAGE_ACCESS_KEY_ID 或 STORAGE_SECRET_ACCESS_KEY")
	}
	return cfg, nil
}

// 报错时不回显原值，以免其中的凭证进入日志。
func checkHTTPURL(name, value string) error {
	if value == "" {
		return fmt.Errorf("缺少 %s", name)
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(value, "?#") {
		return fmt.Errorf("%s 必须是 http 或 https 的绝对地址，不带用户信息、查询串和片段", name)
	}
	return nil
}

// 导出器自己读取这两个变量，地址有误时它并不报错（无法解析时只记一条日志，改为发往本机），所以启动时在这里检查格式。
// 设置了其中任何一个就导出。OTEL_EXPORTER_OTLP_ENDPOINT 是基地址，导出器在它的路径后加上 /v1/traces；
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT 是完整的地址，优先于前者。
func loadTracing() (bool, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	tracesEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if endpoint == "" && tracesEndpoint == "" {
		return false, nil
	}
	if endpoint != "" {
		if err := checkHTTPURL("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint); err != nil {
			return false, err
		}
		if u, err := url.Parse(endpoint); err == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1/traces") {
			return false, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT 的路径不能以 /v1/traces 结尾：导出器会再加上一次，完整的地址请用 OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
		}
	}
	if tracesEndpoint != "" {
		if err := checkHTTPURL("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", tracesEndpoint); err != nil {
			return false, err
		}
	}
	return true, nil
}

func loadOpsListen() (string, error) {
	return listenAddr("ALISTEN_OPS_LISTEN", ":9090")
}

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func loadLogLevel() (slog.Level, error) {
	value := envOr("ALISTEN_LOG_LEVEL", "info")
	level, ok := logLevels[value]
	if !ok {
		return slog.LevelInfo, fmt.Errorf("ALISTEN_LOG_LEVEL 的值 %q 不合法，可选值为 debug、info、warn、error", value)
	}
	return level, nil
}

func listenAddr(name, fallback string) (string, error) {
	value := envOr(name, fallback)
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("%s 的值 %q 不是合法的监听地址：%w", name, value, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s 的值 %q 中的端口不合法，必须是 1–65535 的整数", name, value)
	}
	return value, nil
}

func envPort(name string, fallback int) (int, error) {
	value := envOr(name, strconv.Itoa(fallback))
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s 的值 %q 不合法，必须是 1–65535 的整数", name, value)
	}
	return port, nil
}

func envOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
