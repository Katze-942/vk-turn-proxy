package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cacggghp/vk-turn-proxy/tunnel"
)

type stdLogger struct{}

func (l *stdLogger) WriteLog(msg string) { log.Println(msg) }

func main() {
	cfg := tunnel.DefaultConfig()

	flag.StringVar(&cfg.TurnHost, "turn", "", "override TURN server ip")
	flag.StringVar(&cfg.TurnPort, "port", "", "override TURN port")
	flag.StringVar(&cfg.Listen, "listen", "127.0.0.1:9000", "listen on ip:port")
	flag.StringVar(&cfg.VkLink, "vk-link", "", "VK calls invite link \"https://vk.com/call/join/...\"")
	flag.StringVar(&cfg.YandexLink, "yandex-link", "", "Yandex telemost invite link \"https://telemost.yandex.ru/j/...\"")
	flag.StringVar(&cfg.PeerAddr, "peer", "", "peer server address (host:port)")
	flag.IntVar(&cfg.NumConnections, "n", 0, "connections to TURN (default 16 for VK, 1 for Yandex)")
	flag.BoolVar(&cfg.UDP, "udp", false, "connect to TURN with UDP")
	flag.BoolVar(&cfg.NoDTLS, "no-dtls", false, "connect without obfuscation. DO NOT USE")
	flag.BoolVar(&cfg.IPv4Only, "4", false, "force IPv4 for all connections")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signalChan
		log.Println("Terminating...")
		cancel()
		select {
		case <-signalChan:
		case <-time.After(5 * time.Second):
		}
		log.Fatal("Exit...")
	}()

	t := tunnel.New()
	t.SetLogger(&stdLogger{})

	if err := t.Start(ctx, cfg); err != nil {
		log.Fatalf("Tunnel error: %v", err)
	}
}
