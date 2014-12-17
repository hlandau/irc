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

type privLevel int
const (
  plNone privLevel = iota
  plVoice
  plHalfop
  plOp
  plAdmin
  plFounder
)

type channel struct {
  JoinChannel
  joinedEpoch uint32
  members map[string]*member
}

type member struct {
  userName string
  nickName string
  hostName string
  privLevel privLevel
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
      members: map[string]*member{},
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
      break
    }

    c.userEnteredChannel(m.Args[0], m.UserName, m.HostName, m.NickName, "", false)

  case "PART", "KICK":
    if len(m.Args) < 2 {
      break
    }

    if m.Args[1] == c.backing().NickName() {
      c.markChannelAsJoined(m.Args[0], false)
      break
    }

    c.userLeftChannel(m.Args[0], m.UserName, m.HostName, m.NickName, (m.Command == "KICK"))

  case "NICK":
    if len(m.Args) < 1 {
      break
    }

    c.userChangedNick(m.NickName, m.Args[0])

  case "PONG":
    trySema(c.pongChan)

  case "352": // RPL_WHOREPLY
    if len(m.Args) < 7 {
      break
    }

    channelName := m.Args[0]
    userName := m.Args[1]
    hostName := m.Args[2]
    //serverName := m.Args[3]
    nickName := m.Args[4]
    flags := m.Args[5]
    //realName := m.Args[6]

    c.userEnteredChannel(channelName, userName, hostName, nickName, flags, true)

  case "315": // RPL_ENDOFWHO

  case "353": // RPL_NAMREPLY
  case "366": // RPL_ENDOFNAMES
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
  ch.members = map[string]*member{}

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
    c.solicitChannelMembers(channelName)
  } else {
    ch.joinedEpoch = 0
    ch.members = map[string]*member{}
    trySema(c.channelsUpdatedChan)
  }
}

func (c *Cl4) solicitChannelMembers(channelName string) {
  c.WriteCmd("WHO", channelName)
}

func (c *Cl4) userEnteredChannel(channelName, userName, hostName, nickName, flags string, live bool) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  ch, ok := c.channels[channelName]
  if !ok {
    return
  }

  pl := parsePrivSigils(flags)

  mi := &member{}
  if mi2, ok := ch.members[nickName]; ok {
    mi = mi2
  }

  mi.userName = userName
  mi.nickName = nickName
  mi.hostName = hostName
  mi.privLevel = pl

  ch.members[nickName] = mi
}

func (c *Cl4) userLeftChannel(channelName, userName, hostName, nickName string, wasKicked bool) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Unlock()

  ch, ok := c.channels[channelName]
  if !ok {
    return
  }

  _, ok = ch.members[nickName]
  if !ok {
    return
  }

  delete(ch.members, nickName)
}

func (c *Cl4) userChangedNick(oldNickName, newNickName string) {
  c.channelsMutex.Lock()
  defer c.channelsMutex.Lock()

  for _, ch := range c.channels {
    mi, ok := ch.members[oldNickName]
    if ok {
      delete(ch.members, oldNickName)
      ch.members[newNickName] = mi
      mi.nickName = newNickName
    }
  }
}

func parsePrivSigils(sigils string) privLevel {
  pl := plNone
  for _, r := range sigils {
    if r == '@' && pl < plOp {
      pl = plOp
    }
    if r == '+' && pl < plVoice {
      pl = plVoice
    }
    if r == '%' && pl < plHalfop {
      pl = plHalfop
    }
    if r == '&' && pl < plAdmin {
      pl = plAdmin
    }
    if r == '~' && pl < plFounder {
      pl = plFounder
    }
  }
  return pl
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
