package ircbase

import (
	"fmt"
	"github.com/hlandau/irc/ircparse"
	"sync"
	"time"
)

// Configuration for a pinger connection wrapper.
type PingConfig struct {
	Interval time.Duration // Default if not specified: 1 minute.
	Timeout  time.Duration // Default if not specified: 2*Interval.
}

type pinger struct {
	cfg                 PingConfig
	underlyingConn      ircparse.Conn
	underlyingConnMutex sync.Mutex
	goodUntil           time.Time
	expecting           map[string]struct{}
	pongChan            chan *ircparse.Message
	closeOnce           sync.Once
	closedChan          chan struct{}
}

func (p *pinger) Close() error {
	p.closeOnce.Do(func() {
		close(p.pongChan)
	})
	<-p.closedChan
	return nil
}

func (p *pinger) WriteMsg(msg *ircparse.Message) error {
	p.underlyingConnMutex.Lock()
	defer p.underlyingConnMutex.Unlock()
	return p.underlyingConn.WriteMsg(msg)
}

func (p *pinger) readMsg() (*ircparse.Message, error) {
	p.underlyingConnMutex.Lock()
	defer p.underlyingConnMutex.Unlock()
	return p.underlyingConn.ReadMsg()
}

func (p *pinger) ReadMsg() (*ircparse.Message, error) {
	msg, err := p.readMsg()
	if err != nil {
		return nil, err
	}

	switch msg.Command {
	case "PING":
		err = p.underlyingConn.WriteMsg(&ircparse.Message{
			Command: "PONG",
			Args:    msg.Args,
		})
		if err != nil {
			return nil, err
		}

		return msg, nil

	case "PONG":
		select {
		case p.pongChan <- msg:
		default:
		}
		return msg, nil

	default:
		return msg, nil
	}
}

func (p *pinger) close() {
	p.underlyingConnMutex.Lock()
	defer p.underlyingConnMutex.Unlock()
	p.underlyingConn.Close()
	close(p.closedChan)
}

func (p *pinger) loop() {
	defer p.close()

	p.goodUntil = time.Now().Add(p.cfg.Timeout)
	p.goodUntil = p.goodUntil.Add(p.cfg.Interval) // first ping is delayed by interval

	i := 0
	for {
		select {
		case m, ok := <-p.pongChan:
			if !ok {
				return
			}
			if len(m.Args) < 2 {
				continue
			}
			if _, ok := p.expecting[m.Args[1]]; !ok {
				continue
			}
			p.goodUntil = time.Now().Add(p.cfg.Timeout)
			delete(p.expecting, m.Args[1])

		case <-time.After(p.cfg.Interval):
			if time.Now().After(p.goodUntil) {
				return
			}

			token := fmt.Sprintf("CL%04d", i)
			err := ircparse.WriteCmd(p.underlyingConn, "PING", token)
			if err != nil {
				return
			}

			p.expecting[token] = struct{}{}
			i = (i + 1) % 10000
		}
	}
}

// Wraps an ircparse.Conn to provide automatic responses to incoming PINGs,
// periodic generation of outgoing PINGs and automatic termination of the
// connection if responses to outgoing PINGs are not received in a timely
// fashion. Failures result in the underlying connection being closed.
//
// After calling this, you should use the returned connection instead of the
// one passed; since the underlying connection will be closed when the returned
// connection fails or is explicitly closed, there ceases to be any need to
// deal with the underlying connection directly.
//
// You must call ReadMsg frequently in order to service the connection. Pings
// will not be serviced if you do not do this.
func NewPinger(underlyingConn ircparse.Conn, cfg PingConfig) (ircparse.Conn, error) {
	if cfg.Interval == 0 {
		cfg.Interval = 1 * time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * cfg.Interval
	}
	p := &pinger{
		cfg:            cfg,
		underlyingConn: underlyingConn,
		expecting:      map[string]struct{}{},
		pongChan:       make(chan *ircparse.Message, 8),
		closedChan:     make(chan struct{}),
	}

	go p.loop()
	return p, nil
}
