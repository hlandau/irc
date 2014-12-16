package cl1
import "io"
import "fmt"
import "net"
import "strings"
import "github.com/hlandau/irc/parse"
import "crypto/tls"

type Cl1 struct {
  conn net.Conn
  parser parse.IRCParser
  buf []byte
  closed bool
}

type Config struct {
  Dialer net.Dialer
  TLSConfig tls.Config
}

func (cfg *Config) setDefaults() {
  cfg.TLSConfig.NextProtos = []string{"irc"}
  if cfg.TLSConfig.MinVersion == 0 {
    cfg.TLSConfig.MinVersion = tls.VersionTLS10
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
      return tls.DialWithDialer(&cfg.Dialer, "tcp", address, &cfg.TLSConfig)
    default:
      return nil, fmt.Errorf("unsupported network scope")
  }
}

func Dial(scope, address string, cfg Config) (*Cl1, error) {
  cfg.setDefaults()

  conn, err := dial(scope, address, cfg)
  if err != nil {
    return nil, err
  }
  return New(conn)
}

func fixupAddress(scope, address string) string {
  a := address
  if strings.LastIndex(address, ":") < 0 {
    a += ":"
  }

  host, port, err := net.SplitHostPort(a)
  if err != nil {
    return address
  }
  if port == "" {
    if scope == "tcp" {
      return net.JoinHostPort(host, "6667")
    } else if scope == "tls" {
      return net.JoinHostPort(host, "6697")
    } else {
      return address
    }
  }
  return address
}

func New(conn net.Conn) (*Cl1, error) {
  c := &Cl1{
    conn: conn,
    buf: make([]byte, 512),
  }

  return c, nil
}

func (c *Cl1) Close() error {
  if c.closed {
    return nil
  }

  c.conn.Close()

  c.closed = true
  return nil
}

func (c *Cl1) WriteMsg(m *parse.IRCMessage) error {
  _, err := io.WriteString(c.conn, m.String())
  return err
}

func (c *Cl1) WriteCmd(cmd string, args ...string) error {
  m := parse.IRCMessage{
    Command: cmd,
    Args: args,
  }

  return c.WriteMsg(&m)
}

func (c *Cl1) ReadMsg() (*parse.IRCMessage, error) {
  for {
    m := c.parser.GetMessage()
    if m != nil {
      return m, nil
    }

    n, err := c.conn.Read(c.buf)
    if err != nil && err != io.EOF {
      return nil, err
    }

    buf := c.buf[0:n]
    err = c.parser.Parse(string(buf))
    if err != nil {
      return nil, err
    }
  }
}
