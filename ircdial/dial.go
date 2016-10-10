// Package ircdial provides utilities for making TCP connections to IRC
// servers.
package ircdial

import (
	"crypto/tls"
	"fmt"
	denet "github.com/hlandau/degoutils/net"
	"net"
)

type Config struct {
	Dialer    net.Dialer
	TLSConfig *tls.Config
}

// Setup

func (cfg *Config) setDefaults() {
	if cfg.TLSConfig == nil {
		cfg.TLSConfig = &tls.Config{}
	}
	cfg.TLSConfig.NextProtos = []string{"irc"}
	if cfg.TLSConfig.MinVersion == 0 {
		cfg.TLSConfig.MinVersion = tls.VersionTLS12
	}
}

func dial(scope, address string, cfg Config) (net.Conn, error) {
	if scope == "" {
		scope = "tls"
	}

	address = fixupAddress(scope, address)

	switch scope {
	case "tcp":
		return cfg.Dialer.Dial(scope, address)
	case "tls":
		return tls.DialWithDialer(&cfg.Dialer, "tcp", address, cfg.TLSConfig)
	default:
		return nil, fmt.Errorf("unsupported network scope")
	}
}

func Dial(scope, address string, cfg Config) (net.Conn, error) {
	cfg.setDefaults()
	return dial(scope, address, cfg)
}

func fixupAddress(scope, address string) string {
	host, port, err := denet.FuzzySplitHostPort(address)
	if err != nil {
		return address
	}

	if port == "" {
		if scope == "tcp" {
			port = "6667"
		} else if scope == "tls" {
			port = "6697"
		} else {
			return address
		}
	}

	return net.JoinHostPort(host, port)
}
