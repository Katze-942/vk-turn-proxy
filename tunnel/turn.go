package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

type connectedUDPConn struct {
	*net.UDPConn
}

func (c *connectedUDPConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Write(p)
}

type getCredsFunc func(string) (string, string, string, error)

type turnConnectionParams struct {
	host     string
	port     string
	link     string
	udp      bool
	getCreds getCredsFunc
}

func (t *Tunnel) oneTurnConnection(ctx context.Context, params *turnConnectionParams, peer *net.UDPAddr, conn2 net.PacketConn) error {
	user, pass, url, err := params.getCreds(params.link)
	if err != nil {
		return fmt.Errorf("failed to get TURN credentials: %w", err)
	}
	urlhost, urlport, err := net.SplitHostPort(url)
	if err != nil {
		return fmt.Errorf("failed to parse TURN server address: %w", err)
	}
	if params.host != "" {
		urlhost = params.host
	}
	if params.port != "" {
		urlport = params.port
	}
	turnServerAddr := net.JoinHostPort(urlhost, urlport)
	network := "udp"
	if t.ipv4Only {
		network = "udp4"
	}
	turnServerUdpAddr, err := net.ResolveUDPAddr(network, turnServerAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve TURN server address: %w", err)
	}
	turnServerAddr = turnServerUdpAddr.String()
	t.logf("TURN server: %s", turnServerUdpAddr.IP)

	var cfg *turn.ClientConfig
	var turnConn net.PacketConn
	ctx1, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if params.udp {
		conn, err := net.DialUDP(network, nil, turnServerUdpAddr)
		if err != nil {
			return fmt.Errorf("failed to connect to TURN server: %w", err)
		}
		defer conn.Close()
		turnConn = &connectedUDPConn{conn}
	} else {
		conn, err := t.dialContext(ctx1, "tcp", turnServerAddr)
		if err != nil {
			return fmt.Errorf("failed to connect to TURN server: %w", err)
		}
		defer conn.Close()
		turnConn = turn.NewSTUNConn(conn)
	}
	var addrFamily turn.RequestedAddressFamily
	if peer.IP.To4() != nil {
		addrFamily = turn.RequestedAddressFamilyIPv4
	} else {
		addrFamily = turn.RequestedAddressFamilyIPv6
	}
	cfg = &turn.ClientConfig{
		STUNServerAddr:         turnServerAddr,
		TURNServerAddr:         turnServerAddr,
		Conn:                   turnConn,
		Username:               user,
		Password:               pass,
		RequestedAddressFamily: addrFamily,
		LoggerFactory:          logging.NewDefaultLoggerFactory(),
	}

	client, err := turn.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create TURN client: %w", err)
	}
	defer client.Close()

	err = client.Listen()
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	relayConn, err := client.Allocate()
	if err != nil {
		return fmt.Errorf("failed to allocate: %w", err)
	}
	defer relayConn.Close()

	t.logf("relayed-address=%s", relayConn.LocalAddr().String())

	wg := sync.WaitGroup{}
	wg.Add(2)
	turnctx, turncancel := context.WithCancel(context.Background())
	context.AfterFunc(turnctx, func() {
		relayConn.SetDeadline(time.Now())
		conn2.SetDeadline(time.Now())
	})
	var addr atomic.Value
	go func() {
		defer wg.Done()
		defer turncancel()
		buf := make([]byte, 1600)
		for {
			select {
			case <-turnctx.Done():
				return
			default:
			}
			n, addr1, err1 := conn2.ReadFrom(buf)
			if err1 != nil {
				t.logf("Failed: %s", err1)
				return
			}

			addr.Store(addr1)

			_, err1 = relayConn.WriteTo(buf[:n], peer)
			if err1 != nil {
				t.logf("Failed: %s", err1)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer turncancel()
		buf := make([]byte, 1600)
		for {
			select {
			case <-turnctx.Done():
				return
			default:
			}
			n, _, err1 := relayConn.ReadFrom(buf)
			if err1 != nil {
				t.logf("Failed: %s", err1)
				return
			}
			addr1, ok := addr.Load().(net.Addr)
			if !ok {
				t.logf("Failed: no listener ip")
				return
			}

			_, err1 = conn2.WriteTo(buf[:n], addr1)
			if err1 != nil {
				t.logf("Failed: %s", err1)
				return
			}
		}
	}()

	wg.Wait()
	relayConn.SetDeadline(time.Time{})
	conn2.SetDeadline(time.Time{})
	return nil
}

func (t *Tunnel) oneTurnConnectionLoop(ctx context.Context, params *turnConnectionParams, peer *net.UDPAddr, connchan <-chan net.PacketConn, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case conn2 := <-connchan:
			select {
			case <-tick:
				if err := t.oneTurnConnection(ctx, params, peer, conn2); err != nil {
					t.logf("%s", err)
				}
			default:
			}
		}
	}
}
