package cl4

import (
	"fmt"
	"github.com/hlandau/degoutils/monitor"
	denet "github.com/hlandau/degoutils/net"
	"github.com/hlandau/irc/cl3"
	"github.com/hlandau/irc/ircparse"
	"github.com/hlandau/xlog"
	"sync"
	"sync/atomic"
	"time"
)

var log, Log = xlog.NewQuiet("irc.cl4")

type Cl4 struct {
	cfg                 Config
	cl3                 *cl3.Cl3
	scope, address      string
	closeOnce           sync.Once
	closeChan           chan struct{}
	stopPingChan        chan struct{}
	txChan              chan *ircparse.Message
	rxChan              chan *ircparse.Message
	rxPingChan          chan *ircparse.Message
	forceDisconnectChan chan struct{}
	closed              uint32
}

type Config struct {
	cl3.Config
	Backoff      denet.Backoff
	PingInterval time.Duration
	PingTimeout  time.Duration
}

func (cfg *Config) setDefaults() {
	if cfg.PingInterval == 0 && cfg.PingTimeout == 0 {
		cfg.PingInterval = 1 * time.Minute
		cfg.PingTimeout = 2 * time.Minute
	} else if cfg.PingInterval != 0 {
		cfg.PingTimeout = 2 * cfg.PingInterval
	} else if cfg.PingTimeout != 0 {
		cfg.PingInterval = cfg.PingTimeout / 2
	}
}

func Dial(scope, address string, cfg Config) (*Cl4, error) {
	c := &Cl4{
		cfg:                 cfg,
		scope:               scope,
		address:             address,
		closeChan:           make(chan struct{}),
		stopPingChan:        make(chan struct{}),
		txChan:              make(chan *ircparse.Message),
		rxChan:              make(chan *ircparse.Message),
		rxPingChan:          make(chan *ircparse.Message),
		forceDisconnectChan: make(chan struct{}),
	}

	c.cfg.setDefaults()

	go c.connectLoop()

	return c, nil
}

func (c *Cl4) pingLoop() {
	var txPingTime, rxPingTime time.Time
	i := 0
	waitingFor := map[string]struct{}{}

	for {
		select {
		case <-time.After(c.cfg.PingInterval):
			if !txPingTime.IsZero() && time.Since(rxPingTime) >= c.cfg.PingTimeout {
				log.Debug("ping timeout")
				trySema(c.forceDisconnectChan)
				return
			}

			tag := fmt.Sprintf("CL%04d", i%10000)
			waitingFor[tag] = struct{}{}
			i++

			err := ircparse.WriteCmd(c, "PING", tag)
			if err != nil {
				return
			}

			txPingTime = time.Now()

		case m := <-c.rxPingChan:
			if len(m.Args) > 0 {
				_, ok := waitingFor[m.Args[0]]
				if ok {
					delete(waitingFor, m.Args[0])
					rxPingTime = time.Now()
				}
			}

		case <-c.stopPingChan:
			return
		case <-c.closeChan:
			return
		}
	}
}

func trySema(c chan<- struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Main lifecycle management loop.
func (c *Cl4) connectLoop() {
	var rxTerminationChan <-chan monitor.Event
	var txTerminationChan <-chan monitor.Event
	connectRequestChan := make(chan struct{}, 1)
	connectRequestChan <- struct{}{}

	for {
		select {
		case <-rxTerminationChan:
			log.Debug("rx exited")
			// rx goroutine has terminated, so we need to
			//   - tear down the connection
			c.clDisconnect()
			//   - coax the tx goroutine into exiting by sending a nil message
			c.txChan <- nil
			//   - ensure that the tx goroutine exits, ensuring that the nil
			//     message we just sent doesn't get consumed by a future
			//     tx goroutine.
			<-txTerminationChan
			txTerminationChan = nil
			//   - schedule reconnection.
			connectRequestChan <- struct{}{}

		case <-txTerminationChan:
			log.Debug("tx exited")
			// tx goroutine has terminated, so we need to
			//   - tear down the connection; this will also cause the
			//     rx goroutine to exit.
			c.clDisconnect()
			//   - ensure that the rx goroutine exits.
			<-rxTerminationChan
			rxTerminationChan = nil
			//   - schedule reconnection.
			connectRequestChan <- struct{}{}

		case <-c.forceDisconnectChan:
			// Sent by ping loop.
			log.Debug("forcing disconnect (ping timeout)")
			c.clDisconnect()
			<-rxTerminationChan
			rxTerminationChan = nil
			c.txChan <- nil
			<-txTerminationChan
			txTerminationChan = nil
			connectRequestChan <- struct{}{}

		case <-connectRequestChan:
			// Perform (re)connection.
			log.Debug("connecting")

			err := c.clConnect()
			if err != nil {
				// Maximum connection attempts reached, permanently fail this logical
				// connection.
				c.Close()
				break
			}

			// Launch the rx/tx goroutines under monitors.
			rxTerminationChan = monitor.Monitor(func() error {
				c.rxLoop()
				return nil
			})

			txTerminationChan = monitor.Monitor(func() error {
				c.txLoop()
				return nil
			})

			go c.pingLoop()

			c.rxChan <- &ircparse.Message{
				Command: "$NEWCONN",
			}

		case <-c.closeChan:
			log.Debug("closing")
			// Closure requested.
			c.clDisconnect()
			return
		}
	}
}

// Goroutine for transmission launched by connectLoop.
func (c *Cl4) txLoop() {
	for m := range c.txChan {
		if m == nil {
			return
		}

		err := c.cl3.WriteMsg(m)
		if err != nil {
			return
		}
	}
}

// Goroutine for reception launched by connectLoop.
func (c *Cl4) rxLoop() {
	for {
		m, err := c.cl3.ReadMsg()
		if err != nil {
			return
		}
		if m.Command == "PONG" {
			c.rxPingChan <- m
		}
		c.rxChan <- m
	}
}

// Make a series of connection attempts. Returns if the maximum number of tries
// are reached.
func (c *Cl4) clConnect() error {
	for {
		err := c.clConnectAttempt()
		if err == nil {
			c.cfg.Backoff.Reset()
			return nil
		}

		if atomic.LoadUint32(&c.closed) != 0 {
			return nil
		}

		if !c.cfg.Backoff.Sleep() {
			return err
		}
	}
}

// Makes a single connection attempt.
func (c *Cl4) clConnectAttempt() error {
	c.clDisconnect()

	cc, err := cl3.Dial(c.scope, c.address, c.cfg.Config)
	if err != nil {
		return err
	}

	c.cl3 = cc
	return nil
}

// If there is currently a connection, disconnects it.
func (c *Cl4) clDisconnect() {
	if c.cl3 == nil {
		return
	}

	c.stopPingChan <- struct{}{}
	c.cl3.Close()
	c.cl3 = nil
}

// Closes the connection.
func (c *Cl4) Close() error {
	c.closeOnce.Do(func() {
		atomic.StoreUint32(&c.closed, 1)
		close(c.closeChan)
	})

	return nil
}

var ErrClosed = fmt.Errorf("connection is closed")

// Writes a message. Returns ErrClosed if the connection has been closed.
func (c *Cl4) WriteMsg(m *ircparse.Message) error {
	if m == nil {
		return fmt.Errorf("cannot send nil message")
	}

	select {
	case c.txChan <- m:
		return nil
	case <-c.closeChan:
		return ErrClosed
	}
}

// Reads a message. Returns ErrClosed if the connection has been closed.
func (c *Cl4) ReadMsg() (*ircparse.Message, error) {
	select {
	case m := <-c.rxChan:
		return m, nil
	case <-c.closeChan:
		return nil, ErrClosed
	}
}
