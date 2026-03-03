package tunnel

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StatusRunning, StatusStopped, etc. are tunnel status constants.
const (
	StatusIdle       = "idle"
	StatusConnecting = "connecting"
	StatusRunning    = "running"
	StatusStopping   = "stopping"
	StatusError      = "error"
)

// Tunnel manages a vk-turn-proxy tunnel session.
type Tunnel struct {
	ipv4Only     bool
	customDialer *net.Dialer
	logger       LogWriter
	status       atomic.Value
	cancel       context.CancelFunc
	mu           sync.Mutex
}

// New creates a new Tunnel instance.
func New() *Tunnel {
	t := &Tunnel{
		customDialer: &net.Dialer{},
		logger:       &defaultLogger{},
	}
	t.status.Store(StatusIdle)
	return t
}

// SetLogger sets the log writer for the tunnel.
func (t *Tunnel) SetLogger(l LogWriter) {
	if l != nil {
		t.logger = l
	}
}

// GetStatus returns the current tunnel status.
func (t *Tunnel) GetStatus() string {
	return t.status.Load().(string)
}

func (t *Tunnel) setStatus(s string) {
	t.status.Store(s)
}

func (t *Tunnel) logf(format string, args ...interface{}) {
	t.logger.WriteLog(fmt.Sprintf(format, args...))
}

func (t *Tunnel) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if t.ipv4Only {
		switch network {
		case "tcp":
			network = "tcp4"
		case "udp":
			network = "udp4"
		}
	}
	return t.customDialer.DialContext(ctx, network, address)
}

// Start begins the tunnel with the given config. It blocks until the context is
// cancelled or an error occurs. The caller should pass a cancellable context
// to control the tunnel lifecycle.
func (t *Tunnel) Start(ctx context.Context, cfg *TunnelConfig) error {
	t.mu.Lock()
	if t.GetStatus() == StatusRunning || t.GetStatus() == StatusConnecting {
		t.mu.Unlock()
		return fmt.Errorf("tunnel is already running")
	}

	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.setStatus(StatusConnecting)
	t.mu.Unlock()

	defer func() {
		t.setStatus(StatusIdle)
		t.cancel = nil
	}()

	err := t.run(ctx, cfg)
	if err != nil && ctx.Err() == nil {
		t.setStatus(StatusError)
		return err
	}
	return nil
}

// Stop gracefully stops the tunnel.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.setStatus(StatusStopping)
		t.cancel()
	}
}

func (t *Tunnel) run(ctx context.Context, cfg *TunnelConfig) error {
	t.ipv4Only = cfg.IPv4Only
	if t.ipv4Only {
		t.customDialer = &net.Dialer{
			Resolver: &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, address string) (net.Conn, error) {
					host, _, _ := net.SplitHostPort(address)
					if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
						address = "1.1.1.1:53"
					}
					d := net.Dialer{Timeout: 5 * time.Second}
					return d.DialContext(ctx, "tcp4", address)
				},
			},
		}
		t.logf("Forcing IPv4 for all connections")
	} else {
		t.customDialer = &net.Dialer{}
	}

	if cfg.PeerAddr == "" {
		return fmt.Errorf("peer address is required")
	}
	peerNetwork := "udp"
	if t.ipv4Only {
		peerNetwork = "udp4"
	}
	peer, err := net.ResolveUDPAddr(peerNetwork, cfg.PeerAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve peer address: %w", err)
	}

	hasVk := cfg.VkLink != ""
	hasYandex := cfg.YandexLink != ""
	if hasVk == hasYandex {
		return fmt.Errorf("need either vk-link or yandex-link (not both, not neither)")
	}

	var link string
	var getCreds getCredsFunc
	n := cfg.NumConnections

	dialCtx := t.dialContext

	if hasVk {
		parts := strings.Split(cfg.VkLink, "join/")
		link = parts[len(parts)-1]
		getCreds = func(l string) (string, string, string, error) {
			return getVkCreds(dialCtx, l)
		}
		if n <= 0 {
			n = 16
		}
	} else {
		parts := strings.Split(cfg.YandexLink, "j/")
		link = parts[len(parts)-1]
		getCreds = func(l string) (string, string, string, error) {
			return getYandexCreds(dialCtx, l)
		}
		if n <= 0 {
			n = 1
		}
	}
	if idx := strings.IndexAny(link, "/?#"); idx != -1 {
		link = link[:idx]
	}

	params := &turnConnectionParams{
		host:     cfg.TurnHost,
		port:     cfg.TurnPort,
		link:     link,
		udp:      cfg.UDP,
		getCreds: getCreds,
	}

	listen := cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:9000"
	}

	listenConnChan := make(chan net.PacketConn)
	listenConn, err := net.ListenPacket("udp", listen)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	context.AfterFunc(ctx, func() {
		listenConn.Close()
	})

	t.logf("Listening on %s", listen)
	t.setStatus(StatusRunning)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case listenConnChan <- listenConn:
			}
		}
	}()

	wg := sync.WaitGroup{}
	tick := time.Tick(100 * time.Millisecond)
	if cfg.NoDTLS {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t.oneTurnConnectionLoop(ctx, params, peer, listenConnChan, tick)
			}()
		}
	} else {
		okchan := make(chan struct{})
		connchan := make(chan net.PacketConn)

		wg.Add(2)
		go func() {
			defer wg.Done()
			t.oneDtlsConnectionLoop(ctx, peer, listenConnChan, connchan, okchan)
		}()
		go func() {
			defer wg.Done()
			t.oneTurnConnectionLoop(ctx, params, peer, connchan, tick)
		}()

		select {
		case <-okchan:
		case <-ctx.Done():
		}
		for i := 0; i < n-1; i++ {
			innerConnchan := make(chan net.PacketConn)
			wg.Add(2)
			go func() {
				defer wg.Done()
				t.oneDtlsConnectionLoop(ctx, peer, listenConnChan, innerConnchan, nil)
			}()
			go func() {
				defer wg.Done()
				t.oneTurnConnectionLoop(ctx, params, peer, innerConnchan, tick)
			}()
		}
	}

	wg.Wait()
	return nil
}
