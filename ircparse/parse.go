// Package ircparse provides an IRC message parser and serializer supporting
// RFC1459 and IRCv3 message tags.
package ircparse

import "fmt"

// An RFC1459 IRC protocol message. Supports IRCv3 tags.
type Message struct {
	// Where :server.name is present, this is "server.name".
	ServerName string

	// Where :nick!user@host is present, this is nick, user and host.
	NickName string
	UserName string
	HostName string

	// Command name (uppercase) or three-digit numeric.
	Command string

	// All arguments including trailing argument. Note that the trailing
	// argument is not in any way semantically distinct from the other arguments,
	// but it may contain spaces.
	Args []string

	// IRCv3 tags. nil if no tags were specified. Tags without values have empty string values.
	// @tag1=tagvalue;tag2=tagvalue
	Tags map[string]string
}

// True iff message has a server name.
func (m *Message) IsFromServer() bool {
	return m.ServerName != ""
}

// True iff message has a nickname.
func (m *Message) IsFromClient() bool {
	return m.NickName != ""
}

// Marshal a message as an RFC1459 protocol line, including any IRCv3 message
// tags if specified.
func (m *Message) String() string {
	s := ""
	if len(m.Tags) > 0 {
		s += "@"
		first := true
		for k, v := range m.Tags {
			if first {
				first = false
			} else {
				s += ";"
			}
			s += k
			if len(v) > 0 {
				s += "="
				s += v
			}
		}
		s += " "
	}

	if m.ServerName != "" {
		s += ":"
		s += m.ServerName
		s += " "
	} else if m.NickName != "" {
		s += ":"
		s += m.NickName
		s += "!"
		s += m.UserName
		s += "@"
		s += m.HostName
		s += " "
	}

	s += m.Command

	if len(m.Args) > 0 {
		a := m.Args[0 : len(m.Args)-1]
		for _, v := range a {
			s += " "
			s += v
		}

		ta := m.Args[len(m.Args)-1]
		s += " :"
		s += ta
	}

	s += "\r\n"
	return s
}

type parseState int

const (
	psDRIFTING parseState = iota
	psFROMSTART
	psFROMCONT
	psFROMSERVERNPSTART
	psFROMSERVERNPCONT
	psFROMUSERUSTART
	psFROMUSERUCONT
	psFROMUSERHSTART
	psFROMUSERHCONT
	psFROMUSERHNPSTART
	psFROMUSERHNPCONT
	psFROMUSERHIPV6
	psCOMMANDSTART
	psCOMMANDCONT
	psCOMMANDNCONT
	psCOMMANDNCONT2
	psPREARGSTART
	psARGSTART
	psARGCONT
	psTARGCONT
	psEXPECTLF
	psEND

	psTAGKEY
	psTAGVALUE
	psAFTERTAGS
)

// RFC1459 IRC message parser supporting IRCv3 message tags.
type Parser struct {
	state parseState
	s     string
	k     string // tag key
	msgs  []*Message
	m     *Message

	// The number of malformed messages that have been received.
	MalformedMessageCount int
}

var errMalformedMessage = fmt.Errorf("Malformed IRC protocol message")

// Retrieves an array of parsed messages. The internal slice of such mssages
// is then cleared, so subsequent calls to GetMessages() will return an empty slice.
func (p *Parser) PopMessages() []*Message {
	k := p.msgs
	p.msgs = p.msgs[0:0]
	return k
}

// Retrieves a message. The next message will be returned on the next call. nil
// is returned if there are no more messages.
func (p *Parser) PopMessage() *Message {
	if len(p.msgs) == 0 {
		return nil
	}
	k := p.msgs[0]
	p.msgs = p.msgs[1:]
	return k
}

// Parse arbitrary IRC protocol input. This does not need to be line-aligned.
//
// Complete messages are placed in an internal slice and can be retrieved
// by calling GetMessages().
//
// Malformed messages are skipped until their terminating newline, and parsing
// continues from there. The MalformedMessageCount is incremented.
func (p *Parser) Parse(s string) (err error) {
	if p.m == nil {
		p.m = &Message{}
	}

	recovery := false
	for _, c := range s {
		if recovery {
			if c == '\n' {
				recovery = false
			}
			continue
		}

		err = p.pdispatch(c)
		if err != nil {
			// error recovery: start ignoring until the end of the line
			p.state = psDRIFTING
			p.s = ""
			p.m = &Message{}
			recovery = true
			p.MalformedMessageCount++
		}
	}

	return nil
}

func (p *Parser) pdispatch(c rune) error {
	//log.Info(fmt.Sprintf("state %+v", p.state))
	switch p.state {
	case psDRIFTING, psAFTERTAGS:
		if c == ':' {
			p.state = psFROMSTART
		} else if c == '@' && p.state != psAFTERTAGS {
			p.state = psTAGKEY
		} else {
			// psCOMMANDSTART
			p.state = psCOMMANDSTART
			return p.pdispatch(c) // reissue
		}

	case psTAGKEY:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '/' || c == '-' || c == '.' {
			p.s += string(c)
		} else if c == ';' || c == ' ' {
			if p.m.Tags == nil {
				p.m.Tags = map[string]string{}
			}

			p.m.Tags[p.s] = ""
			p.s = ""
			if c == ' ' {
				p.state = psAFTERTAGS
			}
		} else if c == '=' {
			p.k = p.s
			p.s = ""
			p.state = psTAGVALUE
		} else {
			return errMalformedMessage
		}

	case psTAGVALUE:
		if c == ';' || c == ' ' {
			if p.m.Tags == nil {
				p.m.Tags = map[string]string{}
			}

			p.m.Tags[p.k] = p.s
			p.s = ""
			p.k = ""
			if c == ';' {
				p.state = psTAGKEY
			} else {
				p.state = psAFTERTAGS
			}
		} else if c == 0 || c == '\r' || c == '\n' {
			return errMalformedMessage
		} else {
			p.s += string(c)
		}

	case psFROMSTART:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			p.s += string(c)
			p.state = psFROMCONT
		} else {
			return errMalformedMessage
		}

	case psFROMCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			p.s += string(c)
		} else if c == '.' {
			p.s += string(c)
			p.state = psFROMSERVERNPSTART
		} else if c == '!' {
			p.m.NickName = p.s
			p.state = psFROMUSERUSTART
			p.s = ""
		} else if c == ' ' {
			p.m.ServerName = p.s
			p.state = psCOMMANDSTART
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMSERVERNPSTART:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			p.s += string(c)
			p.state = psFROMSERVERNPCONT
		} else if c == ' ' {
			// server name with trailing .
			p.m.ServerName = p.s[0 : len(p.s)-1]
			p.state = psCOMMANDSTART
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMSERVERNPCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			p.s += string(c)
		} else if c == '.' {
			p.s += string(c)
			p.state = psFROMSERVERNPSTART
		} else if c == ' ' {
			p.m.ServerName = p.s
			p.state = psCOMMANDSTART
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERUSTART:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '~' {
			p.s += string(c)
		} else if c == '@' {
			p.m.UserName = p.s
			p.state = psFROMUSERHSTART
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERUCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '~' {
			p.s += string(c)
		} else if c == '@' {
			p.m.UserName = p.s
			p.state = psFROMUSERHSTART
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERHSTART:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			p.s += string(c)
			p.state = psFROMUSERHCONT
		} else if c == ' ' {
			p.state = psCOMMANDSTART
			p.m.HostName = p.s
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERHCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '/' /* Freenode vhost */ {
			p.s += string(c)
		} else if c == '.' {
			p.s += string(c)
			p.state = psFROMUSERHNPSTART
		} else if c == ' ' {
			p.state = psCOMMANDSTART
			p.m.HostName = p.s
			p.s = ""
		} else if c == ':' {
			p.s += string(c)
			p.state = psFROMUSERHIPV6
		} else {
			return errMalformedMessage
		}

	case psFROMUSERHNPSTART:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '/' /* Freenode vhost */ {
			p.s += string(c)
			p.state = psFROMUSERHNPCONT
		} else if c == ' ' {
			p.state = psCOMMANDSTART
			p.m.HostName = p.s
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERHNPCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '/' /* Freenode vhost */ {
			p.s += string(c)
		} else if c == '.' {
			p.s += string(c)
			p.state = psFROMUSERHNPSTART
		} else if c == ' ' {
			p.state = psCOMMANDSTART
			p.m.HostName = p.s
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psFROMUSERHIPV6:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || /* Charybdis host masking uses full alpha */
			c == ':' {
			p.s += string(c)
		} else if c == ' ' {
			p.state = psCOMMANDSTART
			p.m.HostName = p.s
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psCOMMANDSTART:
		if c >= '0' && c <= '9' {
			p.s += string(c)
			p.state = psCOMMANDNCONT
		} else if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			p.s += string(c)
			p.state = psCOMMANDCONT
		} else {
			return errMalformedMessage
		}

	case psCOMMANDCONT:
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.s += string(c)
		} else if c == ' ' {
			p.m.Command = p.s
			p.state = psARGSTART
			p.s = ""
		} else if c == '\r' {
			p.m.Command = p.s
			p.state = psEXPECTLF
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psCOMMANDNCONT:
		if c >= '0' && c <= '9' {
			p.s += string(c)
			p.state = psCOMMANDNCONT2
		} else {
			return errMalformedMessage
		}

	case psCOMMANDNCONT2:
		if c >= '0' && c <= '9' {
			p.s += string(c)
			p.state = psPREARGSTART
			p.m.Command = p.s
			p.s = ""
		} else {
			return errMalformedMessage
		}

	case psPREARGSTART:
		if c == ' ' {
			p.state = psARGSTART
		} else if c == '\r' {
			p.state = psEXPECTLF
		} else {
			return errMalformedMessage
		}

	case psARGSTART:
		if c == ':' {
			p.state = psTARGCONT
		} else if c == '\r' || c == '\n' || c == '\x00' || c == ' ' {
			return errMalformedMessage
		} else {
			p.s += string(c)
			p.state = psARGCONT
		}

	case psARGCONT:
		if c == ' ' || c == '\r' {
			p.m.Args = append(p.m.Args, p.s)
			p.s = ""
			if c == '\r' {
				p.state = psEXPECTLF
			} else {
				p.state = psARGSTART
			}
		} else if c == '\n' || c == '\x00' {
			return errMalformedMessage
		} else {
			p.s += string(c)
		}

	case psTARGCONT:
		if c == '\r' {
			p.m.Args = append(p.m.Args, p.s)
			p.s = ""
			p.state = psEXPECTLF
		} else if c == '\n' || c == '\x00' {
			return errMalformedMessage
		} else {
			p.s += string(c)
		}

	case psEXPECTLF:
		if c != '\n' {
			return errMalformedMessage
		} else {
			p.state = psDRIFTING
			p.msgs = append(p.msgs, p.m)
			p.m = &Message{}
		}
	}

	return nil
}

// © 2014 Hugo Landau <hlandau@devever.net>    GPLv3 or later
