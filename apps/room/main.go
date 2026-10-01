// room:自托管协作房间服务(socket.io 线协议,persist-then-relay)。
// 配置自 Nacos 配置中心(dataid excalidraw-room.yaml),服务自注册
// Nacos naming(ephemeral);--nacos-addr 为空时回退环境变量。
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfg, err := Load(os.Args[1:])
	if err != nil {
		log.Fatalf("[room] fatal: %v", err)
	}

	server, err := NewRoomServer(cfg)
	if err != nil {
		log.Fatalf("[room] fatal: %v", err)
	}
	if err := server.Run(); err != nil {
		log.Fatalf("[room] fatal: %v", err)
	}

	// listener 就绪后再注册(注册即可服务);指定 nacos 旗标才启用。
	// 旗标已在 Load 里解析过一次,这里重取仅用于命名客户端引导。
	if opts, err := parseFlags(os.Args[1:]); err == nil && opts.addr != "" {
		server.registry = NewRegistry(cfg, opts.addr, opts.namespace)
		if server.registry != nil {
			server.registry.Register(cfg.Port)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	server.Shutdown()
}
