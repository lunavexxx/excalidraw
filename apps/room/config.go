// 配置:--nacos-addr 指定时从 Nacos 配置中心读取全部配置(与 apps/api
// 同款引导模式,dataid 独立为 excalidraw-room.yaml);为空回退环境变量,
// 供测试与脱机调试。消除 JWT_SECRET/INTERNAL_TOKEN 在 Nacos 与 compose
// env 的双源维护——密钥只落在 Nacos,compose 只传 Nacos 引导参数。
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"gopkg.in/yaml.v3"
)

const (
	defaultNacosPort = 8848
	defaultDataID    = "excalidraw-room.yaml"
	defaultGroup     = "DEFAULT_GROUP"

	// Nacos 容器(JVM)启动需要几十秒,room 紧随其后拉配置时重试等待。
	fetchAttempts = 30
	fetchInterval = 3 * time.Second

	defaultPort                 = 3002
	defaultMaxHTTPBufferSize    = 6 * 1024 * 1024
	defaultRateLimitPerSecond   = 150
	defaultConnLimitPerIdentity = 50
	defaultPersistQueueMax      = 10000
)

type Config struct {
	Port                       int
	RedisURL                   string
	DatabaseURL                string
	JWTSecret                  string
	GoAPIURL                   string // 形如 http://api:8080/api/v1
	InternalToken              string
	CORSOrigins                []string // dev 跨端口直连需要;生产同源(经 Caddy)为空
	MaxHTTPBufferSize          int64
	RateLimitPerSecond         int
	PerIdentityConnectionLimit int
	PersistQueueMax            int
	ServiceName                string // Nacos 服务注册名
}

type nacosOptions struct {
	addr      string
	namespace string
	dataID    string
	group     string
}

// Load 从 args 解析配置。--nacos-addr 非空时从 Nacos 拉取完整配置(客户端
// 凭据取自 NACOS_USERNAME / NACOS_PASSWORD 环境变量);为空回退环境变量。
func Load(args []string) (*Config, error) {
	opts, err := parseFlags(args)
	if err != nil {
		return nil, err
	}
	var cfg *Config
	if opts.addr == "" {
		cfg = loadFromEnv()
	} else {
		cfg, err = loadFromNacos(opts)
		if err != nil {
			return nil, err
		}
	}
	if err := requireConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseFlags(args []string) (nacosOptions, error) {
	fs := flag.NewFlagSet("room", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("nacos-addr", "", "Nacos server as host[:port]; empty reads config from env")
	namespace := fs.String("nacos-namespace", "", "Nacos namespace ID")
	dataID := fs.String("nacos-dataid", defaultDataID, "Nacos data ID")
	group := fs.String("nacos-group", defaultGroup, "Nacos group")
	if err := fs.Parse(args); err != nil {
		return nacosOptions{}, err
	}
	return nacosOptions{addr: *addr, namespace: *namespace, dataID: *dataID, group: *group}, nil
}

// requireConfig 必填项校验;room 无任何可选降级路径(fail-fast)。
func requireConfig(cfg *Config) error {
	missing := make([]string, 0, 5)
	for name, v := range map[string]string{
		"redis_url":      cfg.RedisURL,
		"database_url":   cfg.DatabaseURL,
		"jwt_secret":     cfg.JWTSecret,
		"go_api_url":     cfg.GoAPIURL,
		"internal_token": cfg.InternalToken,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "excalidraw-room"
	}
	return nil
}

func loadFromEnv() *Config {
	return &Config{
		Port:                       envInt("PORT", defaultPort),
		RedisURL:                   os.Getenv("REDIS_URL"),
		DatabaseURL:                os.Getenv("DATABASE_URL"),
		JWTSecret:                  os.Getenv("JWT_SECRET"),
		GoAPIURL:                   strings.TrimRight(os.Getenv("GO_API_URL"), "/"),
		InternalToken:              os.Getenv("INTERNAL_TOKEN"),
		CORSOrigins:                splitOrigins(os.Getenv("CORS_ORIGINS")),
		MaxHTTPBufferSize:          int64(envInt("MAX_HTTP_BUFFER_SIZE", defaultMaxHTTPBufferSize)),
		RateLimitPerSecond:         envInt("RATE_LIMIT_PER_SECOND", defaultRateLimitPerSecond),
		PerIdentityConnectionLimit: envInt("PER_USER_CONNECTION_LIMIT", defaultConnLimitPerIdentity),
		PersistQueueMax:            envInt("PERSIST_QUEUE_MAX", defaultPersistQueueMax),
	}
}

func loadFromNacos(opts nacosOptions) (*Config, error) {
	host, port, err := parseAddr(opts.addr)
	if err != nil {
		return nil, err
	}
	content, err := fetchConfig(host, port, opts)
	if err != nil {
		return nil, err
	}
	return parseConfigYAML(content)
}

func parseAddr(addr string) (string, int, error) {
	if !strings.Contains(addr, ":") {
		return addr, defaultNacosPort, nil
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("invalid --nacos-addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return "", 0, fmt.Errorf("invalid --nacos-addr %q: bad port %q", addr, portStr)
	}
	return host, port, nil
}

func fetchConfig(host string, port int, opts nacosOptions) (string, error) {
	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig: &constant.ClientConfig{
			NamespaceId:         opts.namespace,
			Username:            os.Getenv("NACOS_USERNAME"),
			Password:            os.Getenv("NACOS_PASSWORD"),
			TimeoutMs:           5000,
			NotLoadCacheAtStart: true,
			LogLevel:            "error",
			LogDir:              filepath.Join(os.TempDir(), "nacos", "log"),
			CacheDir:            filepath.Join(os.TempDir(), "nacos", "cache"),
		},
		ServerConfigs: []constant.ServerConfig{{
			Scheme:      "http",
			ContextPath: "/nacos",
			IpAddr:      host,
			Port:        uint64(port),
		}},
	})
	if err != nil {
		return "", fmt.Errorf("create nacos client: %w", err)
	}

	for attempt := 1; ; attempt++ {
		content, err := client.GetConfig(vo.ConfigParam{
			DataId: opts.dataID,
			Group:  opts.group,
		})
		if err == nil && strings.TrimSpace(content) != "" {
			return content, nil
		}
		if err == nil {
			err = errors.New("empty config " + opts.dataID + "/" + opts.group + " in namespace " + strconv.Quote(opts.namespace) + " — run deploy/init-nacos.sh first")
		}
		if attempt >= fetchAttempts {
			return "", fmt.Errorf("get config from nacos after %d attempts: %w", attempt, err)
		}
		log.Printf("nacos: fetch config failed (attempt %d/%d): %v; retrying in %s",
			attempt, fetchAttempts, err, fetchInterval)
		time.Sleep(fetchInterval)
	}
}

type yamlConfig struct {
	Port                       string `yaml:"port"`
	RedisURL                   string `yaml:"redis_url"`
	DatabaseURL                string `yaml:"database_url"`
	JWTSecret                  string `yaml:"jwt_secret"`
	GoAPIURL                   string `yaml:"go_api_url"`
	InternalToken              string `yaml:"internal_token"`
	CORSOrigins                string `yaml:"cors_origins"`
	MaxHTTPBufferSize          int64  `yaml:"max_http_buffer_size"`
	RateLimitPerSecond         int    `yaml:"rate_limit_per_second"`
	PerIdentityConnectionLimit int    `yaml:"per_identity_connection_limit"`
	PersistQueueMax            int    `yaml:"persist_queue_max"`
	ServiceName                string `yaml:"nacos_service_name"`
}

func parseConfigYAML(content string) (*Config, error) {
	var raw yamlConfig
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse nacos config yaml: %w", err)
	}
	cfg := &Config{
		Port:                       envOrDefault(raw.Port, defaultPort),
		RedisURL:                   raw.RedisURL,
		DatabaseURL:                raw.DatabaseURL,
		JWTSecret:                  raw.JWTSecret,
		GoAPIURL:                   strings.TrimRight(raw.GoAPIURL, "/"),
		InternalToken:              raw.InternalToken,
		CORSOrigins:                splitOrigins(raw.CORSOrigins),
		MaxHTTPBufferSize:          orDefault64(raw.MaxHTTPBufferSize, defaultMaxHTTPBufferSize),
		RateLimitPerSecond:         orDefault(raw.RateLimitPerSecond, defaultRateLimitPerSecond),
		PerIdentityConnectionLimit: orDefault(raw.PerIdentityConnectionLimit, defaultConnLimitPerIdentity),
		PersistQueueMax:            orDefault(raw.PersistQueueMax, defaultPersistQueueMax),
		ServiceName:                raw.ServiceName,
	}
	return cfg, nil
}

func envOrDefault(raw string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
		return n
	}
	return fallback
}

func orDefault(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func orDefault64(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}

// splitOrigins 解析逗号分隔的允许跨域来源;空白与空段丢弃。
func splitOrigins(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var origins []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			origins = append(origins, part)
		}
	}
	return origins
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
