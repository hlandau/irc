package ircbase

import "github.com/hlandau/irc/ircparse"

type channelInfo struct {
	Name string
	Key  string
}

type autojoin struct {
	underlyingConn ircparse.Conn
	channels       map[string]*channelInfo
}

func NewAutoJoin(underlyingConn ircparse.Conn) (ircparse.Conn, error) {
	j := &autojoin{
		underlyingConn: underlyingConn,
		channels:       map[string]*channelInfo{},
	}
	return j, nil
}

func (j *autojoin) WriteMsg(msg *ircparse.Message) error {
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
				ch = &channelInfo{
					Name: name,
				}
				j.channels[name] = ch
			}
			if key != "" {
				ch.Key = key
			}
		}

	case "PART":
		if len(msg.Args) > 0 {
			delete(j.channels, msg.Args[0])
		}

	default:
	}

	return j.underlyingConn.WriteMsg(msg)
}

func (j *autojoin) ReadMsg() (*ircparse.Message, error) {
	msg, err := j.underlyingConn.ReadMsg()
	if err != nil {
		return nil, err
	}

	switch msg.Command {
	case "001":
		for chanName, ch := range j.channels {
			args := []string{chanName}
			if ch.Key != "" {
				args = append(args, ch.Key)
			}
			ircparse.WriteCmd(j.underlyingConn, "JOIN", args...) // ignore error
		}

	case "471": // :server 471 nick #channel :Channel is full
	case "473": // :server 473 nick #channel :Cannot join channel (+i) - you must be invited
	case "474": // :server 474 nick #channel :Banned from channel
	case "475": // :server 475 nick #channel :Bad channel key
	default:
	}

	return msg, nil
}

func (j *autojoin) Close() error {
	return j.underlyingConn.Close()
}
