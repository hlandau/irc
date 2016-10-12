// Package ircbase provides basic IRC client functionality.
package ircbase

import (
	"github.com/hlandau/irc/ircparse"
	"github.com/hlandau/xlog"
	"io"
	"net"
	"strings"
	"sync"
)

var log, Log = xlog.NewQuiet("irc.base")

// A basic IRC connection. Doesn't handle pings or anything else by itself; a
// raw, low-level message stream.
type Conn struct {
	conn                  net.Conn
	parser                ircparse.Parser
	buf                   []byte
	readMutex, writeMutex sync.Mutex
}

// Creates a new low-level IRC connection over an underlying transport,
// typically a TCP connection.
func New(conn net.Conn) (*Conn, error) {
	c := &Conn{
		conn: conn,
		buf:  make([]byte, 512),
	}

	return c, nil
}

// Closes the underlying connection.
func (c *Conn) Close() error {
	c.conn.Close()
	return nil
}

// Writes a message to the underlying connection. Implements ircparse.Sink.
func (c *Conn) WriteMsg(m *ircparse.Message) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	s := m.String()
	_, err := io.WriteString(c.conn, s)
	log.Debugf("TX: %v", strings.TrimRight(s, "\r\n"))
	return err
}

// Reads a message from the underlying connection. Implements ircparse.Source.
func (c *Conn) ReadMsg() (*ircparse.Message, error) {
	c.readMutex.Lock()
	defer c.readMutex.Unlock()

	for {
		m := c.parser.PopMessage()
		if m != nil {
			log.Debugf("RX: %v", strings.TrimRight(m.String(), "\r\n"))
			return m, nil
		}

		n, err := c.conn.Read(c.buf)
		if err != nil && n == 0 {
			return nil, err
		}

		buf := c.buf[0:n]
		err = c.parser.Parse(string(buf))
		if err != nil {
			return nil, err
		}
	}
}
