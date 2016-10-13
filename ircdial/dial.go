// Package ircdial provides utilities for making TCP or TLS connections to IRC
// servers.
package ircdial

import (
	"crypto/tls"
	"fmt"
	denet "github.com/hlandau/degoutils/net"
	"net"
)

// Configuration for dialing. A zero-valued struct is a valid config.
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

// Dials the server given by address. The address should be in the form
// "hostname:port". If a port is not specified, a sensible default is used
// (6667 or 66697).
//
// scope should be "tcp" or "tls". The returned net.Conn is a raw TCP or TLS
// socket.
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
