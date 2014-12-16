package cl4
import "fmt"
import "sync"
import "sync/atomic"
import "time"
import "unsafe"
import "github.com/hlandau/irc/cl3"
import "github.com/hlandau/irc/parse"
import denet "github.com/hlandau/degoutils/net"

type Cl4 struct {
  cfg Config
  cl3 unsafe.Pointer
  scope, address string
  replaceWaitChan chan struct{}
  replacer uint32
  epoch uint32
  closed uint32

  channelsMutex sync.Mutex
  channels map[string]*channel
  channelsUpdatedChan chan struct{}
  closeChan chan struct{}
  pongChan chan struct{}
}

type channel struct {
  JoinChannel
  joinedEpoch uint32
}

type Config struct {
  cl3.Config
  Backoff denet.Backoff
  JoinChannels []JoinChannel
  PingInterval time.Duration
  PingTimeout time.Duration
}

func (cfg *Config) setDefaults() {
  if cfg.PingInterval == 0 && cfg.PingTimeout == 0 {
    cfg.PingInterval = 1*time.Minute
    cfg.PingTimeout  = 2*time.Minute
  } else if cfg.PingInterval != 0 {
    cfg.PingTimeout = 2*cfg.PingInterval
  } else if cfg.PingTimeout != 0 {
    cfg.PingInterval = cfg.PingTimeout/2
  }
}

type JoinChannel struct {
  Channel string
  Key string
}

func Dial(scope, address string, cfg Config) (*Cl4, error) {
  c := &Cl4{
    cfg: cfg,
    scope: scope,
    address: address,
    epoch: 1,
    replaceWaitChan: make(chan struct{}),
    channels: map[string]*channel{},
    channelsUpdatedChan: make(chan struct{}, 1),
    closeChan: make(chan struct{}),
    pongChan: make(chan struct{}),
  }

  c.cfg.setDefaults()

  for _, jc := range c.cfg.JoinChannels {
    ch := &channel{
      JoinChannel: jc,
    }
    c.channels[jc.Channel] = ch
  }

  cc, err := cl3.Dial(scope, address, c.cfg.Config)
  if err != nil {
    return nil, err
  }

  c.cl3 = unsafe.Pointer(cc)

  go c.joinLoop()
  go c.pingLoop()

  return c, nil
}

func (c *Cl4) backing() *cl3.Cl3 {
  return (*cl3.Cl3)(atomic.LoadPointer(&c.cl3))
}

func (c *Cl4) Close() error {
  if atomic.AddUint32(&c.closed, 1) != 1 {
    return nil
  }

  close(c.channelsUpdatedChan)
  close(c.closeChan)
  return c.backing().Close()
}

func (c *Cl4) WriteCmd(cmd string, args ...string) error {
  for {
    err := c.backing().WriteCmd(cmd, args...)
    if err == nil {
      return nil
    }

    err = c.replaceClient()
    if err != nil {
      return err
    }
  }
}

func (c *Cl4) WriteMsg(m *parse.IRCMessage) error {
  for {
    err := c.backing().WriteMsg(m)
    if err == nil {
      c.processOutgoingMsgInternally(m)
      return nil
    }

    err = c.replaceClient()
    if err != nil {
      return err
    }
  }
}

func (c *Cl4) ReadMsg() (*parse.IRCMessage, error) {
  for {
    m, err := c.backing().ReadMsg()
    if err == nil {
      c.processIncomingMsgInternally(m)
      return m, nil
    }

    err = c.replaceClient()
    if err != nil {
      return nil, err
    }
  }
}

func (c *Cl4) processOutgoingMsgInternally(m *parse.IRCMessage) {
  switch m.Command {
  case "JOIN":
    if len(m.Args) < 1 {
      break
    }

    key := ""
    if len(m.Args) > 1 {
      key = m.Args[1]
    }

    c.addChannelJoin(m.Args[0], key)

  case "PART":
    if len(m.Args) < 1 {
      break
    }

    c.removeChannelJoin(m.Args[0])
  }
}

func (c *Cl4) processIncomingMsgInternally(m *parse.IRCMessage) {
  switch m.Command {
  case "JOIN":
    if len(m.Args) < 1 {
      break
    }

    if m.NickName == c.backing().NickName() {
      c.markChannelAsJoined(m.Args[0], true)
    }

  case "PART", "KICK":
    if len(m.Args) < 2 {
      break
    }

    if m.Args[1] == c.backing().NickName() {
      c.markChannelAsJoined(m.Args[0], false)
    }

  case "PONG":
    trySema(c.pongChan)
  }
}

func (c *Cl4) pingLoop() {
  lastPong := time.Now()

  for {
    select {
    case <-c.closeChan:
      return
    case <-c.pongChan:
      lastPong = time.Now()
    case <-time.After(c.cfg.PingInterval):
      if time.Now().After(lastPong.Add(c.cfg.PingTimeout)) {
        c.replaceClient()
        continue
      }

      c.writePing("CL")
    }
  }
}

func (c *Cl4) writePing(args ...string) {
  c.WriteCmd("PING", args...)
}

func (c *Cl4) joinLoop() {
  for {
    if atomic.LoadUint32(&c.closed) != 0 {
      return
    }

    anyNotJoined := c.jlJoinChannels()
    if !anyNotJoined {
      <-c.channelsUpdatedChan
    } else {
      select {
      case <-c.closeChan:
        return
      case <-c.channelsUpdatedChan:
      case <-time.After(1*time.Minute):
      }
    }
  }
}

func (c *Cl4) jlJoinChannels() bool {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  anyNotJoined := false
  for _, ch := range c.channels {
    if ch.joinedEpoch != atomic.LoadUint32(&c.epoch) {
      anyNotJoined = true
      c.writeJoin(ch.Channel, ch.Key)
    }
  }

  return anyNotJoined
}

func (c *Cl4) addChannelJoin(channelName, key string) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  _, ok := c.channels[channelName]
  if ok {
    return
  }

  ch := &channel{}
  ch.Channel = channelName
  ch.Key = key

  c.channels[channelName] = ch

  trySema(c.channelsUpdatedChan)
}

func (c *Cl4) removeChannelJoin(channelName string) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  _, ok := c.channels[channelName]
  if !ok {
    return
  }

  delete(c.channels, channelName)
}

func (c *Cl4) markChannelAsJoined(channelName string, joined bool) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  ch, ok := c.channels[channelName]
  if !ok {
    return
  }

  if joined {
    ch.joinedEpoch = atomic.LoadUint32(&c.epoch)
  } else {
    ch.joinedEpoch = 0
    trySema(c.channelsUpdatedChan)
  }
}

func trySema(c chan struct{}) {
  select {
  case c <- struct{}{}:
  default:
  }
}

func (c *Cl4) writeJoin(channel, key string) error {
  if key == "" {
    return c.WriteCmd("JOIN", channel)
  }
  return c.WriteCmd("JOIN", channel, key)
}

var errIsClosed = fmt.Errorf("client is closed")

func (c *Cl4) replaceClient() error {
  if atomic.LoadUint32(&c.closed) != 0 {
    return errIsClosed
  }

  for {
    epoch := atomic.LoadUint32(&c.epoch)
    replacer := atomic.LoadUint32(&c.replacer)
    if replacer >= epoch {
      break
    }

    if !atomic.CompareAndSwapUint32(&c.replacer, replacer, epoch) {
      continue
    }

    go c.replaceTask(epoch)
    break
  }

  <-c.replaceWaitChan
  if atomic.LoadUint32(&c.closed) != 0 {
    return errIsClosed
  }

  return nil
}

func (c *Cl4) replaceTask(epoch uint32) {
  c.backing().Close()

  for {
    if !c.cfg.Backoff.Sleep() {
      // Max retries exceeded.
      c.Close()
      return
    }

    cc, err := cl3.Dial(c.scope, c.address, c.cfg.Config)
    if err != nil {
      continue
    }

    c.pongChan <- struct{}{}
    atomic.StorePointer(&c.cl3, unsafe.Pointer(cc))
    c.cfg.Backoff.Reset()

    atomic.StoreUint32(&c.epoch, epoch+1)
    close(c.replaceWaitChan)
    c.replaceWaitChan = make(chan struct{})

    trySema(c.channelsUpdatedChan)
  }
}
