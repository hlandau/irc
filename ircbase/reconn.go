package ircbase

import (
	"fmt"
	denet "github.com/hlandau/goutils/net"
	"github.com/hlandau/irc/ircparse"
	"sync"
	"sync/atomic"
)

var (
	// Returned by the ReadMsg and WriteMsg methods of a reconnecter Conn if a
	// maximum number of reconnection attempts is configured and that limit is
	// reached.
	ErrMaxReconnectAttempts = fmt.Errorf("maximum reconnection attempts reached")

	// Returned by the ReadMsg and WriteMsg methods of a reconnected Conn after
	// Close is called.
	ErrClosed = fmt.Errorf("connection has been closed")
)

// Configuration for a Reconnecter.
type ReconnecterConfig struct {
	Backoff denet.Backoff // Determines reconnection interval.
}

type reconnecter struct {
	cfg                  ReconnecterConfig
	connFunc             func() (ircparse.Conn, error)
	connFailedChan       chan struct{}
	shutdownCompleteChan chan struct{}
	shutdownError        error
	requestShutdownChan  chan struct{}
	requestShutdownOnce  sync.Once
	shutdown             uint32

	requestReadChan  chan chan<- readResponse
	requestWriteChan chan writeRequest
	readQueue        []chan<- readResponse
	writeQueue       []writeRequest
	//currentConn sync.Mutex
	//currentConnMutex ircparse.Conn
}

var errNotConnected = fmt.Errorf("not currently connected")

// A reconnecter uses connFunc to create connection stacks. These stacks should
// be an ircparse.Conn which is the top of a layer of ircparse.Conns (such as
// pingers, etc.) The stack may be constructed arbitrarily.
//
// connFunc will be called whenever a new connection attempt is made.
//
// The returned reconnecter Conn will block WriteMsg calls until the message
// can be successfully written to an IRC connection (which is not a guarantee
// that it will be delivered, only that the OS kernel has accepted it into its
// TCP buffer). ReadMsg will block until a new message is available, waiting
// for a new connection to be made if necessary.
//
// If the reconnecter exhibits permanent failure due to a retry limit set in
// cfg.Backoff, any current and future calls to ReadMsg WriteMsg will return
// with ErrMaxReconnectAttempts.
//
// Essentially, a reconnecter conn uses a sequence of underlying connections
// which it creates to create the appearance of a persistent, reliable message
// stream. (In practice, incoming and outgoing messages may be lost when
// underlying connections fail.)
//
// Calling Close terminates any current connection, ceases reconnection attempts
// and any future call to ReadMsg or WriteMsg will return ErrClosed. To reconnect
// after calling Close, create a new reconnecter.
func NewReconnecter(cfg ReconnecterConfig, connFunc func() (ircparse.Conn, error)) (ircparse.Conn, error) {
	r := &reconnecter{
		cfg:                  cfg,
		connFunc:             connFunc,
		connFailedChan:       make(chan struct{}, 1),
		requestShutdownChan:  make(chan struct{}),
		shutdownCompleteChan: make(chan struct{}),
		requestReadChan:      make(chan chan<- readResponse),
		requestWriteChan:     make(chan writeRequest),
	}

	go r.loop()
	return r, nil
}

func (r *reconnecter) shouldShutdown() bool {
	return atomic.LoadUint32(&r.shutdown) != 0
}

func (r *reconnecter) loop() {
	defer func() {
		if r.shutdownError == nil {
			r.shutdownError = ErrClosed
		}
		close(r.shutdownCompleteChan)
	}()
	defer r.failQueues()

	first := true
	for {
		if r.shouldShutdown() {
			return
		}

		if !first {
			if !r.cfg.Backoff.Sleep() {
				r.shutdownError = ErrMaxReconnectAttempts
				return
			}
		} else {
			first = false
		}

		conn, err := r.connFunc()
		if err != nil {
			continue
		}

		// Got connection.
		r.connLoop(conn)
	}
}

func (r *reconnecter) connLoop(conn ircparse.Conn) {
	defer conn.Close()

	// Loop.
	for {
		if r.shouldShutdown() {
			return
		}

		// Handle queued outgoing messages.
		err := r.handleWriteQueue(conn)
		if err != nil {
			return
		}

		err = r.handleReadQueue(conn)
		if err != nil {
			return
		}

		select {
		case resChan := <-r.requestReadChan:
			r.readQueue = append(r.readQueue, resChan)

		case req := <-r.requestWriteChan:
			r.writeQueue = append(r.writeQueue, req)

		case <-r.requestShutdownChan:
			return
		}
	}
}

func (r *reconnecter) handleWriteQueue(conn ircparse.Conn) error {
	for len(r.writeQueue) > 0 {
		req := r.writeQueue[0]
		err := conn.WriteMsg(req.Message)
		if err != nil {
			return err
		}

		req.ResultChan <- nil
		r.writeQueue = r.writeQueue[1:]
	}

	return nil
}

func (r *reconnecter) handleReadQueue(conn ircparse.Conn) error {
	for len(r.readQueue) > 0 {
		resChan := r.readQueue[0]
		msg, err := conn.ReadMsg()
		if err != nil {
			return err
		}

		switch msg.Command {
		case "001":
			r.cfg.Backoff.Reset()
		default:
		}

		resChan <- readResponse{msg, nil}
		r.readQueue = r.readQueue[1:]
	}

	return nil
}

func (r *reconnecter) failQueues() {
	for _, req := range r.writeQueue {
		req.ResultChan <- ErrClosed
	}
	for _, resChan := range r.readQueue {
		resChan <- readResponse{nil, ErrClosed}
	}

	r.writeQueue = nil
	r.readQueue = nil
}

func (r *reconnecter) WriteMsg(msg *ircparse.Message) error {
	errChan := make(chan error, 1)
	select {
	case r.requestWriteChan <- writeRequest{msg, errChan}:
		return <-errChan
	case <-r.shutdownCompleteChan:
		return r.shutdownError
	}
}

func (r *reconnecter) ReadMsg() (*ircparse.Message, error) {
	responseChan := make(chan readResponse, 1)
	select {
	case r.requestReadChan <- responseChan:
		res := <-responseChan
		return res.Message, res.Error
	case <-r.shutdownCompleteChan:
		return nil, r.shutdownError
	}
}

func (r *reconnecter) Close() error {
	r.requestShutdownOnce.Do(func() {
		close(r.requestShutdownChan)
		atomic.StoreUint32(&r.shutdown, 1)
	})
	<-r.shutdownCompleteChan
	return r.shutdownError
}
