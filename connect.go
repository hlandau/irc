// Package irc provides an IRC client library composed of layered services.
package irc

import (
	"github.com/hlandau/irc/ircbase"
	"github.com/hlandau/irc/ircdial"
	"github.com/hlandau/irc/ircparse"
)

// Configuration for Dial. A zero-valued configuration is valid, but you should
// at least set a nickname in RegisterConfig.
type Config struct {
	Dial     ircdial.Config
	Ping     ircbase.PingConfig
	Register ircbase.RegisterConfig
}

// Returns an IRC server connection which provides ping handling, initial
// registration, auto-reconnect and channel rejoin services by composing layers
// implemented in the ircbase package. The functionality provided by this
// function will suffice for most use cases without needing to manually compose
// the functionality implemented in ircbase.
//
// scope should be "tcp" or "tls". addr should be "hostname" or
// "hostname:port". The default ports are 6667 and 6697.
func Dial(scope, addr string, cfg Config) (ircparse.Conn, error) {
	conn, err := ircbase.NewReconnecter(ircbase.ReconnecterConfig{}, func() (ircparse.Conn, error) {
		c, err := ircdial.Dial(scope, addr, cfg.Dial)
		if err != nil {
			return nil, err
		}

		var ic ircparse.Conn
		ic, err = ircbase.NewConn(c)
		if err != nil {
			return nil, err
		}

		ic, err = ircbase.NewPinger(ic, cfg.Ping)
		if err != nil {
			return nil, err
		}

		ic, err = ircbase.NewRegisterer(ic, cfg.Register)
		if err != nil {
			return nil, err
		}

		return ic, nil
	})
	if err != nil {
		return nil, err
	}

	conn, err = ircbase.NewAutoJoin(conn)
	return conn, err
}
