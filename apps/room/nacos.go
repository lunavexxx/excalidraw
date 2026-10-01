// Nacos 服务注册(naming):room 启动并开始监听后把自己注册为临时
// 实例(ephemeral,SDK 经 gRPC keepalive 保活,连接断开服务端自动摘除),
// 关闭时主动注销。注册失败不阻塞服务(Caddy 走静态路由,注册是运维
// 可见性/未来服务发现的增强),后台按 registerRetryInterval 重试。
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

const registerRetryInterval = 5 * time.Second

type Registry struct {
	cfg       *Config
	host      string
	port      int
	namespace string
	client    naming_client.INamingClient
	stop      chan struct{}
	stopped   chan struct{}
}

// NewRegistry 建立 naming 客户端;失败只告警(注册为尽力而为)。
func NewRegistry(cfg *Config, addr, namespace string) *Registry {
	host, port, err := parseAddr(addr)
	if err != nil {
		log.Printf("[nacos] invalid nacos addr %q: %v", addr, err)
		return nil
	}
	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig: &constant.ClientConfig{
			NamespaceId:         namespace,
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
		log.Printf("[nacos] create naming client: %v (service will run unregistered)", err)
		return nil
	}
	return &Registry{cfg: cfg, host: host, port: port, namespace: namespace, client: client,
		stop: make(chan struct{}), stopped: make(chan struct{})}
}

// Register 注册本实例;后台重试直至成功或服务退出。
// 调用时机:HTTP listener 已就绪之后(注册即视为可服务)。
func (r *Registry) Register(listenPort int) {
	ip, err := privateIP()
	if err != nil {
		log.Printf("[nacos] discover private ip: %v (service will run unregistered)", err)
		close(r.stopped)
		return
	}
	go func() {
		defer close(r.stopped)
		for attempt := 1; ; attempt++ {
			ok, err := r.client.RegisterInstance(vo.RegisterInstanceParam{
				ServiceName: r.cfg.ServiceName,
				GroupName:   defaultGroup,
				ClusterName: "DEFAULT",
				Ip:          ip,
				Port:        uint64(listenPort),
				Weight:      1,
				Enable:      true,
				Healthy:     true,
				Ephemeral:   true, // gRPC 会话保活;宕机实例由服务端自动摘除
				Metadata: map[string]string{
					"app":      "excalidraw-room",
					"protocol": "socket.io",
				},
			})
			if err == nil && ok {
				log.Printf("[nacos] registered %s %s:%d (namespace %s, ephemeral)",
					r.cfg.ServiceName, ip, listenPort, r.namespace)
				return
			}
			if attempt == 1 || attempt%12 == 0 {
				log.Printf("[nacos] register failed (attempt %d): %v (ok=%v); retrying in %s",
					attempt, err, ok, registerRetryInterval)
			}
			select {
			case <-r.stop:
				return
			case <-time.After(registerRetryInterval):
			}
		}
	}()
}

// Deregister 主动注销;失败仅告警(ephemeral 实例会被服务端兜底摘除)。
func (r *Registry) Deregister() {
	if r == nil {
		return
	}
	close(r.stop)
	select {
	case <-r.stopped:
	case <-time.After(2 * time.Second):
	}
	if r.client == nil {
		return
	}
	ip, err := privateIP()
	if err != nil {
		return
	}
	ok, err := r.client.DeregisterInstance(vo.DeregisterInstanceParam{
		ServiceName: r.cfg.ServiceName,
		GroupName:   defaultGroup,
		Ip:          ip,
		Port:        uint64(r.cfg.Port),
		Ephemeral:   true,
	})
	if err != nil || !ok {
		log.Printf("[nacos] deregister failed (ephemeral instance will expire server-side): err=%v ok=%v", err, ok)
	} else {
		log.Printf("[nacos] deregistered %s %s:%d", r.cfg.ServiceName, ip, r.cfg.Port)
	}
}

// privateIP 取首个 RFC1918 私网 IPv4(compose 容器网络的常规情形)。
func privateIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil || !ipNet.IP.IsPrivate() {
			continue
		}
		return ipNet.IP.String(), nil
	}
	return "", fmt.Errorf("no private ipv4 address found")
}
