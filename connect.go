package irc

import (
	"github.com/hlandau/irc/ircbase"
	"github.com/hlandau/irc/ircdial"
	"github.com/hlandau/irc/ircparse"
)

type Config struct {
	Dial     ircdial.Config
	Ping     ircbase.PingConfig
	Register ircbase.RegisterConfig
}

func Dial(scope, addr string, cfg Config) (ircparse.Conn, error) {
	conn, err := ircbase.NewReconnecter(ircbase.ReconnecterConfig{}, func() (ircparse.Conn, error) {
		c, err := ircdial.Dial(scope, addr, cfg.Dial)
		if err != nil {
			return nil, err
		}

		var ic ircparse.Conn
		ic, err = ircbase.New(c)
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
