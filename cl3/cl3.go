package cl3
import "fmt"
import "time"
import "regexp"
import "strings"
import "strconv"
import "math/rand"
import "sync"
import "sync/atomic"
import "github.com/hlandau/irc/cl2"
import "github.com/hlandau/irc/parse"

type Cl3 struct {
  cfg Config
  cl2 *cl2.Cl2
  stashedMsgs []*parse.IRCMessage
  stashedMsgsLen int32
  stashedMsgsMutex sync.Mutex
  actualNickName string
  actualNickNameMutex sync.Mutex
}

type Config struct {
  cl2.Config
  NickName string
  UserName string
  RealName string
  ServerPassword string
  NickservPassword string
}

func (cfg *Config) setDefaults() {
  gNick := (cfg.NickName != "")
  gUser := (cfg.UserName != "")
  gReal := (cfg.RealName != "")

  if !gNick && !gUser && !gReal { // 000
    cfg.NickName = randomNick()
    cfg.UserName = cfg.NickName
    cfg.RealName = cfg.NickName
  } else if !gNick && !gUser &&  gReal { // 001
    if isValidUserName(cfg.RealName) {
      cfg.NickName = cfg.RealName
      cfg.UserName = cfg.RealName
    } else {
      cfg.NickName = randomNick()
      cfg.UserName = cfg.NickName
    }
  } else if !gNick &&  gUser && !gReal { // 010
    cfg.NickName = cfg.UserName
    cfg.RealName = cfg.UserName
  } else if !gNick &&  gUser &&  gReal { // 011
    cfg.NickName = cfg.UserName
  } else if  gNick && !gUser && !gReal { // 100
    cfg.UserName = cfg.NickName
    cfg.RealName = cfg.NickName
  } else if  gNick && !gUser &&  gReal { // 101
    cfg.UserName = cfg.NickName
  } else if  gNick &&  gUser && !gReal { // 110
    cfg.RealName = cfg.UserName
  } // else 111
}

var re_validUserName = regexp.MustCompilePOSIX(`^[a-zA-Z0-9_-]{1,30}$`)

func isValidUserName(name string) bool {
  return re_validUserName.MatchString(name)
}

var srand = rand.New(rand.NewSource(time.Now().Unix()))

func randomNick() string {
  return fmt.Sprintf("Guest%d", srand.Uint32() & 0xFFFF)
}

func Dial(scope, address string, cfg Config) (*Cl3, error) {
  c := &Cl3{
    cfg: cfg,
  }
  c.cfg.setDefaults()

  cc, err := cl2.Dial(scope, address, cfg.Config)
  if err != nil {
    return nil, err
  }

  c.cl2 = cc
  err = c.register()
  if err != nil {
    return nil, err
  }

  return c, nil
}

func (c *Cl3) register() error {
  c.actualNickName = c.cfg.NickName

  if c.cfg.ServerPassword != "" {
    err := c.cl2.WriteCmd("PASS", c.cfg.ServerPassword)
    if err != nil {
      return err
    }
  }

  err := c.cl2.WriteCmd("NICK", c.actualNickName)
  if err != nil {
    return err
  }

  err = c.cl2.WriteCmd("USER", c.cfg.UserName, "-", "-", c.cfg.RealName)
  if err != nil {
    return err
  }

loop:
  for {
    m, err := c.cl2.ReadMsg()
    if err != nil {
      return err
    }

    c.stashedMsgs = append(c.stashedMsgs, m)
    c.stashedMsgsLen = int32(len(c.stashedMsgs))

    switch m.Command {
    case "001": // RPL_WELCOME
      // Welcome to IRC.
    case "002": // RPL_YOURHOST
      // Your host is <servername>, running version <version>
    case "003": // RPL_CREATED
      // This server was created <date>
    case "004": // RPL_MYINFO
      // <servername> <version> <usermodes> <chanmodes>
    case "005": // RPL_ISUPPORT (one or more)
      // ... :are supported by this server
    case "251": // RPL_LUSERCLIENT
      // There are <num> users and <num> invisible on <num> servers
    case "372": // RPL_MOTD
      // MOTD Line
    case "375": // RPL_MOTDSTART
      // - <server> Message of the day -
    case "376": // RPL_ENDOFMOTD
      // Consider registration once ENDOFMOTD is received.
      if c.cfg.NickservPassword != "" {
        err := c.cl2.WriteCmd("PRIVMSG", "NickServ", fmt.Sprintf("IDENTIFY %s", c.cfg.NickservPassword))
        if err != nil {
          return err
        }
      }
      break loop
    case "433": // RPL_NICKNAMEINUSE
      // The nickname we sent is in use. Try another.
      c.actualNickName = alternateNickName(c.cfg.NickName, c.actualNickName)
      err := c.cl2.WriteCmd("NICK", c.actualNickName)
      if err != nil {
        return err
      }
    case "463": // RPL_NOPERMFORHOST
      return fmt.Errorf("client host is not authorized to connect to server")
    case "464": // RPL_PASSWDMISMATCH
      // :<reason>
      // The server password was incorrect.
      return fmt.Errorf("server password incorrect")
    case "465": // RPL_YOUREBANNEDCREEP
      return fmt.Errorf("client is banned from server")
    }
  }

  return nil
}

func (c *Cl3) NickName() string {
  c.actualNickNameMutex.Lock()
  defer c.actualNickNameMutex.Unlock()

  return c.actualNickName
}

// Concurrency safe.
func (c *Cl3) Close() error {
  return c.cl2.Close()
}

// Concurrency safe.
func (c *Cl3) WriteMsg(m *parse.IRCMessage) error {
  return c.cl2.WriteMsg(m)
}

// Concurrency safe.
func (c *Cl3) WriteCmd(cmd string, args ...string) error {
  return c.cl2.WriteCmd(cmd, args...)
}

// Concurrency safe.
func (c *Cl3) ReadMsg() (*parse.IRCMessage, error) {
  m, err := c.readMsg()
  if err != nil {
    return m, err
  }

  c.processReceivedMsgInternally(m)

  return m, nil
}

func (c *Cl3) processReceivedMsgInternally(m *parse.IRCMessage) error {
  switch m.Command {
    case "NICK":
      if len(m.Args) == 0 {
        break
      }
      c.actualNickNameMutex.Lock()
      defer c.actualNickNameMutex.Unlock()
      c.actualNickName = m.Args[0]
  }
  return nil
}

func (c *Cl3) readMsg() (*parse.IRCMessage, error) {
  if atomic.LoadInt32(&c.stashedMsgsLen) != 0 {
    c.stashedMsgsMutex.Lock()
    defer c.stashedMsgsMutex.Unlock()
    m := c.stashedMsgs[0]
    c.stashedMsgs = c.stashedMsgs[1:]
    atomic.AddInt32(&c.stashedMsgsLen, -1)
    return m, nil
  }

  return c.cl2.ReadMsg()
}

func alternateNickName(preferred, current string) string {
  if !strings.HasPrefix(current, preferred) {
    return preferred
  }

  c := current[len(preferred):]
  if c == "" {
    return preferred+"_"
  } else if c == "_" {
    return preferred+"__"
  } else if c == "__" {
    return preferred+"1"
  } else {
    n, err := strconv.ParseUint(c, 10, 16)
    if err != nil {
      return preferred
    }
    return fmt.Sprintf("%s%d", preferred, n+1)
  }
}