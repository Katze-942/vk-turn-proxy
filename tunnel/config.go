package tunnel

// TunnelConfig holds all configuration for a tunnel session.
type TunnelConfig struct {
	// VkLink is the VK call invite link (e.g. "https://vk.com/call/join/...").
	// Mutually exclusive with YandexLink.
	VkLink string

	// YandexLink is the Yandex Telemost invite link (e.g. "https://telemost.yandex.ru/j/...").
	// Mutually exclusive with VkLink.
	YandexLink string

	// PeerAddr is the remote server address (host:port).
	PeerAddr string

	// Listen is the local listen address (default "127.0.0.1:9000").
	Listen string

	// TurnHost overrides the TURN server host.
	TurnHost string

	// TurnPort overrides the TURN server port.
	TurnPort string

	// NumConnections is the number of parallel TURN connections.
	// 0 means auto (16 for VK, 1 for Yandex).
	NumConnections int

	// UDP controls whether to connect to TURN with UDP instead of TCP.
	UDP bool

	// NoDTLS disables DTLS encryption.
	NoDTLS bool

	// IPv4Only forces IPv4 for all connections.
	IPv4Only bool
}

// DefaultConfig returns a TunnelConfig with default values.
func DefaultConfig() *TunnelConfig {
	return &TunnelConfig{
		Listen: "127.0.0.1:9000",
	}
}
