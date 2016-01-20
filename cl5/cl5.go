package cl5

import (
	"github.com/hlandau/irc/cl4"
	"github.com/hlandau/irc/ircparse"
	"sync"
)

// Cl5.
type Cl5 struct {
	cfg Config
	cl4 *cl4.Cl4

	stateMutex   sync.Mutex
	joinChannels map[string]string // name: channel name, value: key
}

type Config struct {
	cl4.Config
}

func Dial(scope, address string, cfg Config) (*Cl5, error) {
	c := &Cl5{
		cfg:          cfg,
		joinChannels: map[string]string{},
	}

	var err error
	c.cl4, err = cl4.Dial(scope, address, c.cfg.Config)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Closes the connection.
func (c *Cl5) Close() error {
	return c.cl4.Close()
}

// Writes a message.
func (c *Cl5) WriteMsg(m *ircparse.Message) error {
	c.onOutgoing(m)
	return c.cl4.WriteMsg(m)
}

// Reads a message.
func (c *Cl5) ReadMsg() (*ircparse.Message, error) {
	m, err := c.cl4.ReadMsg()
	if err != nil {
		return nil, err
	}

	if m.Command == "$NEWCONN" {
		c.onNewConn()
	}

	return m, nil
}

func (c *Cl5) onNewConn() error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	for channelName, channelKey := range c.joinChannels {
		c.writeJoin(channelName, channelKey)
	}

	return nil
}

func (c *Cl5) onOutgoing(m *ircparse.Message) error {
	switch m.Command {
	case "JOIN":
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()

		if len(m.Args) > 0 {
			channelName := m.Args[0]
			channelKey := ""
			if len(m.Args) > 1 {
				channelKey = m.Args[1]
			}
			c.joinChannels[channelName] = channelKey
		}
	case "PART":
		c.stateMutex.Lock()
		defer c.stateMutex.Unlock()

		if len(m.Args) > 0 {
			delete(c.joinChannels, m.Args[0])
		}

	default:
	}

	return nil
}

func (c *Cl5) writeJoin(channelName, channelKey string) error {
	if channelKey != "" {
		return ircparse.WriteCmd(c.cl4, "JOIN", channelName, channelKey)
	}

	return ircparse.WriteCmd(c.cl4, "JOIN", channelName)
}
