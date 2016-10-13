// Package ircbase provides basic IRC client functionality in the form of a
// basic IRC protocol connection, and layers on top of that interface which
// implement basic services such as ping handling, registration, reconnection
// and channel rejoining.
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
type llConn struct {
	conn                  net.Conn
	parser                ircparse.Parser
	buf                   []byte
	readMutex, writeMutex sync.Mutex
}

// Creates a new low-level IRC connection over an underlying transport,
// typically a TCP or TLS connection.
func NewConn(conn net.Conn) (ircparse.Conn, error) {
	c := &llConn{
		conn: conn,
		buf:  make([]byte, 512),
	}

	return c, nil
}

// Closes the underlying connection.
func (c *llConn) Close() error {
	c.conn.Close()
	return nil
}

// Writes a message to the underlying connection. Implements ircparse.Sink.
func (c *llConn) WriteMsg(m *ircparse.Message) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	s := m.String()
	_, err := io.WriteString(c.conn, s)
	log.Debugf("TX: %v", strings.TrimRight(s, "\r\n"))
	return err
}

// Reads a message from the underlying connection. Implements ircparse.Source.
func (c *llConn) ReadMsg() (*ircparse.Message, error) {
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
