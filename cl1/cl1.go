// Packae cl1 provides low-level IRC connectivity.
//
// It does not handle reconnection, PINGs, login or do anything on its own.
package cl1

import "io"
import "fmt"
import "net"
import "github.com/hlandau/irc/ircparse"
import denet "github.com/hlandau/degoutils/net"
import "crypto/tls"
import "github.com/hlandau/xlog"
import "strings"

var log, Log = xlog.New("irc.cl1")

type Cl1 struct {
	conn   net.Conn
	parser ircparse.Parser
	buf    []byte
}

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

func Dial(scope, address string, cfg Config) (*Cl1, error) {
	log.Debugf("dialing %s %s", scope, address)

	cfg.setDefaults()

	conn, err := dial(scope, address, cfg)
	if err != nil {
		log.Errore(err, "failed to dial")
		return nil, err
	}
	return New(conn)
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

// Channel

func New(conn net.Conn) (*Cl1, error) {
	c := &Cl1{
		conn: conn,
		buf:  make([]byte, 512),
	}

	return c, nil
}

func (c *Cl1) Close() error {
	log.Debug("closing connection")
	c.conn.Close()
	return nil
}

func (c *Cl1) WriteMsg(m *ircparse.Message) error {
	s := m.String()
	log.Debugf("--> %s", strings.TrimRight(s, "\r\n"))
	_, err := io.WriteString(c.conn, s)
	return err
}

func (c *Cl1) ReadMsg() (*ircparse.Message, error) {
	for {
		m := c.parser.PopMessage()
		if m != nil {
			log.Debugf("<-- %s", strings.TrimRight(m.String(), "\r\n"))
			return m, nil
		}

		n, err := c.conn.Read(c.buf)
		if err != nil && n == 0 {
			log.Errore(err, "connection error")
			return nil, err
		}

		buf := c.buf[0:n]
		err = c.parser.Parse(string(buf))
		if err != nil {
			log.Errore(err, "incoming message parse error")
			return nil, err
		}
	}
}
