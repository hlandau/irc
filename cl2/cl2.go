// Package cl2 provides IRC connectivity layered on top of cl1.
//
// It provides PING/PONG management and abstracts protocol work behind
// channels. It does not handle reconnection or login.
package cl2

import "fmt"
import "time"
import "sync"
import "sync/atomic"
import "github.com/hlandau/irc/cl1"
import "github.com/hlandau/irc/ircparse"
import "github.com/hlandau/xlog"

var log, Log = xlog.New("irc.cl2")

type Cl2 struct {
	cfg        Config
	cl1        *cl1.Cl1
	closed     uint32
	closeOnce  sync.Once
	closeError error
	writeMutex sync.RWMutex
	txChan     chan *ircparse.Message
	rxChan     chan *ircparse.Message
	pongChan   chan struct{}
	lastPong   time.Time
}

type Config struct {
	cl1.Config
	RxQueueSize int
	TxQueueSize int
}

func Dial(scope, address string, cfg Config) (*Cl2, error) {
	if cfg.RxQueueSize == 0 {
		cfg.RxQueueSize = 128
	}
	if cfg.TxQueueSize == 0 {
		cfg.TxQueueSize = 128
	}

	c := &Cl2{
		cfg:      cfg,
		txChan:   make(chan *ircparse.Message, cfg.TxQueueSize),
		rxChan:   make(chan *ircparse.Message, cfg.RxQueueSize),
		pongChan: make(chan struct{}, 1),
	}

	cl1, err := cl1.Dial(scope, address, c.cfg.Config)
	if err != nil {
		return nil, err
	}

	c.cl1 = cl1
	go c.txLoop()
	go c.rxLoop()

	return c, nil
}

func (c *Cl2) txLoop() {
	defer c.cl1.Close()

	for m := range c.txChan {
		err := c.cl1.WriteMsg(m)
		if err != nil {
			c.terminate(err, "tx error")
			break
		}
	}

	c.cl1 = nil
}

func (c *Cl2) rxLoop() {
	defer close(c.rxChan)

	for {
		m, err := c.cl1.ReadMsg()
		if err != nil {
			c.terminate(err, "rx error")
			return
		}

		if m.Command == "PING" {
			c.sendPong(m.Args)
			continue
		}

		c.rxChan <- m
	}
}

func (c *Cl2) sendPong(args []string) error {
	if len(args) == 0 {
		return ircparse.WriteCmd(c, "PONG")
	}

	return ircparse.WriteCmd(c, "PONG", args[0])
}

func (c *Cl2) terminate(err error, msg string) {
	cerr := fmt.Errorf("client was terminated due to %s: %v", msg, err)
	c.close(cerr)
}

func (c *Cl2) close(err error) {
	c.closeOnce.Do(func() {
		c.writeMutex.Lock()
		defer c.writeMutex.Unlock()

		c.closeError = err
		atomic.StoreUint32(&c.closed, 1)
		close(c.txChan)
	})
}

var errIsClosed = fmt.Errorf("client is closed")

func (c *Cl2) Close() error {
	c.close(errIsClosed)
	return nil
}

func (c *Cl2) WriteMsg(m *ircparse.Message) (err error) {
	c.writeMutex.RLock()
	defer c.writeMutex.RUnlock()

	if atomic.LoadUint32(&c.closed) != 0 {
		return c.closeError
	}

	c.txChan <- m
	return nil
}

func (c *Cl2) ReadMsg() (*ircparse.Message, error) {
	for {
		m, ok := <-c.rxChan
		if !ok {
			return nil, c.closeError
		}

		if m == nil {
			continue
		}

		return m, nil
	}
}
