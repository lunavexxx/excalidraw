// Package config 解析 api 服务配置:指定 --nacos-addr 时从 Nacos 配置中心
// 读取全部配置;否则回退到环境变量(PORT / DATABASE_URL),便于本地脱机调试。
package config

import (
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
	defaultDataID    = "excalidraw-api.yaml"
	defaultGroup     = "DEFAULT_GROUP"

	// Nacos 容器(JVM)启动需要几十秒,api 紧随其后拉配置时重试等待。
	fetchAttempts = 30
	fetchInterval = 3 * time.Second
)

type Config struct {
	Port        string
	DatabaseURL string
	// JWTSecret 签发 access token;为空则不注册 auth 路由。
	JWTSecret string
	// PhoneCryptoKey 为 base64 编码的 32 字节主密钥,HKDF 域分离派生出
	// 手机号 AES-GCM 加密与 HMAC 查找哈希两组子密钥;为空则不注册 auth 路由。
	PhoneCryptoKey string
	CORSOrigins    []string
	// InternalToken 供 room 服务回调内部端点(X-Internal-Token);
	// 与 deploy/.env 同源,为空则不注册内部路由。
	InternalToken string
	// canvas 请求体限额(字节);非正值回落到 canvas.DefaultLimits。
	MaxSceneBytes int
	MaxFileBytes  int
}

type nacosOptions struct {
	addr      string
	namespace string
	dataID    string
	group     string
}

// Load 从 args 解析配置。--nacos-addr 非空时从 Nacos 拉取完整配置(客户端
// 凭据取自 NACOS_USERNAME / NACOS_PASSWORD 环境变量,避免出现在命令行);
// 为空时回退到环境变量。
func Load(args []string) (Config, error) {
	opts, err := parseFlags(args)
	if err != nil {
		return Config{}, err
	}
	if opts.addr == "" {
		return loadFromEnv(), nil
	}
	return loadFromNacos(opts)
}

func parseFlags(args []string) (nacosOptions, error) {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
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

func loadFromEnv() Config {
	return Config{
		Port:           getenv("PORT", "8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		PhoneCryptoKey: os.Getenv("PHONE_CRYPTO_KEY"),
		CORSOrigins:    splitOrigins(os.Getenv("CORS_ORIGINS")),
		InternalToken:  os.Getenv("INTERNAL_TOKEN"),
		MaxSceneBytes:  getIntEnv("CANVAS_SCENE_MAX_BYTES", 0),
		MaxFileBytes:   getIntEnv("CANVAS_FILE_MAX_BYTES", 0),
	}
}

func loadFromNacos(opts nacosOptions) (Config, error) {
	host, port, err := parseAddr(opts.addr)
	if err != nil {
		return Config{}, err
	}
	content, err := fetchConfig(host, port, opts)
	if err != nil {
		return Config{}, err
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
			err = fmt.Errorf("empty config %s/%s in namespace %q — run deploy/init-nacos.sh first",
				opts.dataID, opts.group, opts.namespace)
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
	Port           string `yaml:"port"`
	DatabaseURL    string `yaml:"database_url"`
	JWTSecret      string `yaml:"jwt_secret"`
	PhoneCryptoKey string `yaml:"phone_crypto_key"`
	CORSOrigins    string `yaml:"cors_origins"`
	InternalToken  string `yaml:"internal_token"`
	MaxSceneBytes  int    `yaml:"canvas_scene_max_bytes"`
	MaxFileBytes   int    `yaml:"canvas_file_max_bytes"`
}

func parseConfigYAML(content string) (Config, error) {
	var raw yamlConfig
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return Config{}, fmt.Errorf("parse nacos config yaml: %w", err)
	}
	cfg := Config{
		Port:           raw.Port,
		DatabaseURL:    raw.DatabaseURL,
		JWTSecret:      raw.JWTSecret,
		PhoneCryptoKey: raw.PhoneCryptoKey,
		CORSOrigins:    splitOrigins(raw.CORSOrigins),
		InternalToken:  raw.InternalToken,
		MaxSceneBytes:  raw.MaxSceneBytes,
		MaxFileBytes:   raw.MaxFileBytes,
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	return cfg, nil
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getIntEnv 读整型环境变量;缺省或非法时返回 fallback。
func getIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
