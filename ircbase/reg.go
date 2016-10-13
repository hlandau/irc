package ircbase

import (
	"fmt"
	"github.com/hlandau/irc/ircparse"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registerer wraps a connection and manages the registration sequence.
//
// If registration fails, the connection is terminated and further attempts to
// use the connection result in an error. If registration succeeds, you will
// receive the registration success typical messages, 001 etc.
type Registerer struct {
	cfg              RegisterConfig
	requestReadChan  chan chan readResponse
	requestWriteChan chan writeRequest
	//requestCloseChan chan chan error

	requestCloseOnce sync.Once
	requestCloseChan chan struct{}

	shutdownError        error
	shutdownCompleteChan chan struct{}

	// Loop goroutine use only.
	underlyingConn ircparse.Conn

	regDone     bool
	currentNick string
}

// Configuration options for a Registerer.
type RegisterConfig struct {
	UserName         string // Username to register with.
	NickName         string // Nickname to register with.
	RealName         string // Realname to register with.
	ServerPassword   string // Server password to use, if any.
	NickServPassword string // If set, identify to nickserv.
}

func (cfg *RegisterConfig) setDefaults() {
	gNick := (cfg.NickName != "")
	gUser := (cfg.UserName != "")
	gReal := (cfg.RealName != "")

	if !gNick && !gUser && !gReal { // 000
		cfg.NickName = randomNick()
		cfg.UserName = cfg.NickName
		cfg.RealName = cfg.NickName
	} else if !gNick && !gUser && gReal { // 001
		if isValidUserName(cfg.RealName) {
			cfg.NickName = cfg.RealName
			cfg.UserName = cfg.RealName
		} else {
			cfg.NickName = randomNick()
			cfg.UserName = cfg.NickName
		}
	} else if !gNick && gUser && !gReal { // 010
		cfg.NickName = cfg.UserName
		cfg.RealName = cfg.UserName
	} else if !gNick && gUser && gReal { // 011
		cfg.NickName = cfg.UserName
	} else if gNick && !gUser && !gReal { // 100
		cfg.UserName = cfg.NickName
		cfg.RealName = cfg.NickName
	} else if gNick && !gUser && gReal { // 101
		cfg.UserName = cfg.NickName
	} else if gNick && gUser && !gReal { // 110
		cfg.RealName = cfg.UserName
	} // else 111
}

var re_validUserName = regexp.MustCompilePOSIX(`^[a-zA-Z0-9_-]{1,30}$`)

func isValidUserName(name string) bool {
	return re_validUserName.MatchString(name)
}

var srand = rand.New(rand.NewSource(time.Now().Unix()))

func randomNick() string {
	return fmt.Sprintf("Guest%d", srand.Uint32()&0xFFFF)
}

func alternateNickName(preferred, current string) string {
	if !strings.HasPrefix(current, preferred) {
		return preferred
	}

	c := current[len(preferred):]
	if c == "" {
		return preferred + "_"
	} else if c == "_" {
		return preferred + "__"
	} else if c == "__" {
		return preferred + "1"
	} else {
		n, err := strconv.ParseUint(c, 10, 16)
		if err != nil {
			return preferred
		}
		return fmt.Sprintf("%s%d", preferred, n+1)
	}
}

// Creates a new Registerer wrapping the given underlying connection.
func NewRegisterer(underlyingConn ircparse.Conn, cfg RegisterConfig) (ircparse.Conn, error) {
	r := &Registerer{
		cfg:                  cfg,
		underlyingConn:       underlyingConn,
		requestReadChan:      make(chan chan readResponse),
		requestWriteChan:     make(chan writeRequest),
		requestCloseChan:     make(chan struct{}),
		shutdownCompleteChan: make(chan struct{}),
	}

	r.cfg.setDefaults()

	go r.loop()

	return r, nil
}

func (r *Registerer) loop() {
	defer close(r.shutdownCompleteChan)
	defer func() {
		if r.underlyingConn != nil {
			r.underlyingConn.Close()
		}
	}()

	r.shutdownError = r.loopInner()
}

func (r *Registerer) loopInner() error {
	err := r.register()
	if err != nil {
		return err
	}

	for {
		select {
		case responseChan := <-r.requestReadChan:
			isRegError := false
			msg, err := r.underlyingConn.ReadMsg()
			if err == nil {
				err = r.handleRegMsg(msg)
				isRegError = true
			}
			responseChan <- readResponse{msg, err}
			if err != nil && isRegError {
				return err
			}

		case writeReq := <-r.requestWriteChan:
			err := r.underlyingConn.WriteMsg(writeReq.Message)
			writeReq.ResultChan <- err

		case <-r.requestCloseChan:
			err := r.underlyingConn.Close()
			r.underlyingConn = nil
			return err
		}
	}
}

func (r *Registerer) handleRegMsg(msg *ircparse.Message) error {
	if r.regDone {
		return nil
	}

	switch msg.Command {
	case "001": // RPL_WELCOME
		// Welcome to IRC.
		if r.cfg.NickServPassword != "" {
			// ignore errors
			ircparse.WriteCmd(r.underlyingConn, "PRIVMSG", "NickServ", fmt.Sprintf("IDENTIFY %s", r.cfg.NickServPassword))
		}

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
		// MOTD line
	case "375": // RPL_MOTDSTART
		// - <server> Message of the day -

	case "376": // RPL_ENDOFMOTD
		// Consider registration done once ENDOFMOTD is received.
		r.regDone = true

	case "433": // RPL_NICKNAMEINUSE
		// The nickname we sent is in use. Try another.
		r.currentNick = alternateNickName(r.cfg.NickName, r.currentNick)
		err := ircparse.WriteCmd(r.underlyingConn, "NICK", r.currentNick)
		if err != nil {
			return err
		}

	case "463": // RPL_NOPERMFORHOST
		return fmt.Errorf("client host is not authorized to connect to server: %#v", msg.String())
	case "464": // RPL_PASSWDMISMATCH
		return fmt.Errorf("server password incorrect: %#v", msg.String())
	case "465": // RPL_YOUREBANNEDCREEP
		return fmt.Errorf("client is banned from server: %#v", msg.String())
	}

	return nil
}

func (r *Registerer) register() error {
	r.currentNick = r.cfg.NickName

	if r.cfg.ServerPassword != "" {
		err := ircparse.WriteCmd(r.underlyingConn, "PASS", r.cfg.ServerPassword)
		if err != nil {
			return err
		}
	}

	err := ircparse.WriteCmd(r.underlyingConn, "NICK", r.cfg.NickName)
	if err != nil {
		return err
	}

	err = ircparse.WriteCmd(r.underlyingConn, "USER", r.cfg.UserName, "-", "-", r.cfg.RealName)
	if err != nil {
		return err
	}

	return nil
}

var errClosed = fmt.Errorf("registerer IRC connection was closed")

func (r *Registerer) finalError() error {
	if r.shutdownError == nil {
		return errClosed
	}
	return r.shutdownError
}

func (r *Registerer) WriteMsg(msg *ircparse.Message) error {
	errorChan := make(chan error, 1)
	select {
	case r.requestWriteChan <- writeRequest{msg, errorChan}:
		return <-errorChan
	case <-r.shutdownCompleteChan:
		return r.finalError()
	}
}

func (r *Registerer) ReadMsg() (*ircparse.Message, error) {
	responseChan := make(chan readResponse, 1)
	select {
	case r.requestReadChan <- responseChan:
		response := <-responseChan
		return response.Message, response.Error

	case <-r.shutdownCompleteChan:
		return nil, r.finalError()
	}
}

func (r *Registerer) Close() error {
	r.requestCloseOnce.Do(func() {
		close(r.requestCloseChan)
	})
	<-r.shutdownCompleteChan
	return r.shutdownError
}
