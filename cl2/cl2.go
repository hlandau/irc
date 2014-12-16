package cl2
import "fmt"
import "time"
import "sync"
import "sync/atomic"
import "github.com/hlandau/irc/cl1"
import "github.com/hlandau/irc/parse"

type Cl2 struct {
  cfg Config
  cl1 *cl1.Cl1
  closed uint32
  closeMutex sync.Mutex
  closeError error
  txChan chan *parse.IRCMessage
  rxChan chan *parse.IRCMessage
  closeChan chan struct{}
  pongChan chan struct{}
  lastPong time.Time
}

type Config struct {
  cl1.Config
}

func Dial(scope, address string, cfg Config) (*Cl2, error) {
  c := &Cl2{
    cfg: cfg,
    txChan: make(chan *parse.IRCMessage, 128),
    rxChan: make(chan *parse.IRCMessage, 128),
    closeChan: make(chan struct{}),
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
  for m := range c.txChan {
    err := c.cl1.WriteMsg(m)
    if err != nil {
      c.terminate(err, "tx error")
      break
    }
  }
  c.cl1.Close()
  c.cl1 = nil
}

func (c *Cl2) rxLoop() {
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
  close(c.rxChan)
}

func (c *Cl2) sendPong(args []string) error {
  if len(args) == 0 {
    return c.WriteCmd("PONG")
  }

  return c.WriteCmd("PONG", args[0])
}

func (c *Cl2) terminate(err error, msg string) {
  cerr := fmt.Errorf("client was terminated due to %s: %v", msg, err)
  c.close(cerr)
}

func (c *Cl2) close(err error) error {
  c.closeMutex.Lock()
  defer c.closeMutex.Unlock()

  if atomic.LoadUint32(&c.closed) != 0 {
    return nil
  }

  c.closeError = err
  atomic.StoreUint32(&c.closed, 1)

  close(c.txChan)
  close(c.closeChan)
  return nil
}

var errIsClosed = fmt.Errorf("client is closed")

func (c *Cl2) Close() error {
  return c.close(errIsClosed)
}

func (c *Cl2) WriteMsg(m *parse.IRCMessage) (err error) {
  if atomic.LoadUint32(&c.closed) != 0 {
    return c.closeError
  }

  // XXX: It is possible for the tx channel to be closed just after we check
  // c.closed, so we could panic here due to writing to a closed channel.
  // Kludgily work around this.
  defer func() {
    if recover() != nil {
      err = errIsClosed
    }
  }()

  c.txChan <- m
  return
}

func (c *Cl2) WriteCmd(cmd string, args ...string) error {
  m := parse.IRCMessage{
    Command: cmd,
    Args: args,
  }
  return c.WriteMsg(&m)
}

func (c *Cl2) ReadMsg() (*parse.IRCMessage, error) {
  for {
    if atomic.LoadUint32(&c.closed) != 0 {
      return nil, c.closeError
    }

    m := <-c.rxChan
    if m == nil {
      continue
    }

    return m, nil
  }
}
