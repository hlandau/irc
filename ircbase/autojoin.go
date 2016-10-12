package ircbase

import (
	"github.com/hlandau/irc/ircparse"
	"sync"
	"time"
)

type channelJoiner struct {
	j                *autojoin
	name             string
	keyMutex         sync.Mutex
	key              string
	joined           bool
	statusChangeChan chan bool
	terminateChan    chan struct{}
}

func newChannelJoiner(j *autojoin, name, key string) *channelJoiner {
	cj := &channelJoiner{
		j:                j,
		name:             name,
		key:              key,
		statusChangeChan: make(chan bool),
		terminateChan:    make(chan struct{}),
	}

	go cj.loop()
	return cj
}

func (cj *channelJoiner) SetKey(key string) {
	cj.keyMutex.Lock()
	defer cj.keyMutex.Unlock()
	cj.key = key
}

func (cj *channelJoiner) getKey() string {
	cj.keyMutex.Lock()
	defer cj.keyMutex.Unlock()
	return cj.key
}

func (cj *channelJoiner) SetJoined(joined bool) {
	cj.statusChangeChan <- joined
}

func (cj *channelJoiner) Terminate() {
	close(cj.terminateChan)
}

func (cj *channelJoiner) loop() {
	for {
		select {
		case joined := <-cj.statusChangeChan:
			cj.joined = joined
			if joined {
				cj.quietLoop()
			} else {
				// if we get a status change to false while false, this means we've
				// reconnected and should try again immediately
				cj.sendJoin()
			}
		case <-time.After(30 * time.Second):
			cj.sendJoin()
		case <-cj.terminateChan:
			return
		}
	}
}

func (cj *channelJoiner) quietLoop() {
	for {
		select {
		case joined := <-cj.statusChangeChan:
			cj.joined = joined
			if !joined {
				cj.sendJoin()
				return
			}
		case <-cj.terminateChan:
			return
		}
	}
}

func (cj *channelJoiner) sendJoin() error {
	args := []string{cj.name}
	key := cj.getKey()
	if key != "" {
		args = append(args, key)
	}
	return ircparse.WriteCmd(cj.j.underlyingConn, "JOIN", args...)
}

//

type autojoin struct {
	requestReadChan  chan chan readResponse
	requestWriteChan chan writeRequest

	requestCloseOnce sync.Once
	requestCloseChan chan struct{}

	shutdownError        error
	shutdownCompleteChan chan struct{}

	underlyingConn ircparse.Conn

	channels    map[string]*channelJoiner
	currentNick string
}

func NewAutoJoin(underlyingConn ircparse.Conn) (ircparse.Conn, error) {
	j := &autojoin{
		underlyingConn:       underlyingConn,
		channels:             map[string]*channelJoiner{},
		requestReadChan:      make(chan chan readResponse),
		requestWriteChan:     make(chan writeRequest),
		requestCloseChan:     make(chan struct{}),
		shutdownCompleteChan: make(chan struct{}),
	}

	go j.loop()

	return j, nil
}

func (j *autojoin) loop() {
	defer close(j.shutdownCompleteChan)
	defer func() {
		if j.underlyingConn != nil {
			j.underlyingConn.Close()
		}
	}()

	j.shutdownError = j.loopInner()
}

func (j *autojoin) loopInner() error {
	for {
		select {
		case responseChan := <-j.requestReadChan:
			msg, err := j.underlyingConn.ReadMsg()
			responseChan <- readResponse{msg, err}
			if err == nil {
				j.handleRx(msg)
			} else {
				return err
			}

		case writeReq := <-j.requestWriteChan:
			err := j.underlyingConn.WriteMsg(writeReq.Message)
			writeReq.ResultChan <- err
			if err == nil {
				j.handleTx(writeReq.Message)
			} else {
				return err
			}

		case <-j.requestCloseChan:
			err := j.underlyingConn.Close()
			j.underlyingConn = nil
			return err
		}
	}
}

func (j *autojoin) handleRx(msg *ircparse.Message) {
	switch msg.Command {
	case "001":
		if len(msg.Args) > 0 {
			j.currentNick = msg.Args[0]
		}
		for _, ch := range j.channels {
			ch.SetJoined(false)
		}

	case "NICK": // our nick has been changed
		if len(msg.Args) > 0 && msg.NickName == j.currentNick {
			j.currentNick = msg.Args[0]
		}

	case "JOIN": // we have joined a channel
		if len(msg.Args) > 0 && msg.NickName == j.currentNick {
			channelName := msg.Args[0]
			ch, ok := j.channels[channelName]
			if ok {
				ch.SetJoined(true)
			}
		}

	case "PART": // we have been parted from a channel, potentially unwillingly
		if len(msg.Args) > 0 && msg.NickName == j.currentNick {
			channelName := msg.Args[0]
			ch, ok := j.channels[channelName]
			if ok {
				ch.SetJoined(false)
			}
		}

	case "KICK": // we have been removed from a channel unwillingly
		if len(msg.Args) >= 2 && msg.Args[1] == j.currentNick {
			channelName := msg.Args[0]
			ch, ok := j.channels[channelName]
			if ok {
				ch.SetJoined(false)
			}
		}

	case "471": // :server 471 nick #channel :Channel is full
	case "473": // :server 473 nick #channel :Cannot join channel (+i) - you must be invited
	case "474": // :server 474 nick #channel :Banned from channel
	case "475": // :server 475 nick #channel :Bad channel key
	}
}

func (j *autojoin) handleTx(msg *ircparse.Message) {
	switch msg.Command {
	case "JOIN":
		if len(msg.Args) > 0 {
			name := msg.Args[0]
			key := ""
			if len(msg.Args) > 1 {
				key = msg.Args[1]
			}

			ch, ok := j.channels[name]
			if !ok {
				ch = newChannelJoiner(j, name, key)
				j.channels[name] = ch
			} else if key != "" {
				ch.SetKey(key)
			}
		}

	case "PART":
		if len(msg.Args) > 0 {
			ch, ok := j.channels[msg.Args[0]]
			if ok {
				ch.Terminate()
				delete(j.channels, msg.Args[0])
			}
		}
	}
}

func (j *autojoin) finalError() error {
	if j.shutdownError == nil {
		return errClosed
	}
	return j.shutdownError
}

func (j *autojoin) WriteMsg(msg *ircparse.Message) error {
	errorChan := make(chan error, 1)
	select {
	case j.requestWriteChan <- writeRequest{msg, errorChan}:
		return <-errorChan
	case <-j.shutdownCompleteChan:
		return j.finalError()
	}
}

func (j *autojoin) ReadMsg() (*ircparse.Message, error) {
	responseChan := make(chan readResponse, 1)
	select {
	case j.requestReadChan <- responseChan:
		response := <-responseChan
		return response.Message, response.Error
	case <-j.shutdownCompleteChan:
		return nil, j.finalError()
	}
}

func (j *autojoin) Close() error {
	j.requestCloseOnce.Do(func() {
		close(j.requestCloseChan)
	})
	<-j.shutdownCompleteChan
	return j.shutdownError
}
