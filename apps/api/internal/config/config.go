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
		Port:        getenv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
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
	Port        string `yaml:"port"`
	DatabaseURL string `yaml:"database_url"`
}

func parseConfigYAML(content string) (Config, error) {
	var raw yamlConfig
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return Config{}, fmt.Errorf("parse nacos config yaml: %w", err)
	}
	cfg := Config{Port: raw.Port, DatabaseURL: raw.DatabaseURL}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
