package ircparse

import "io"

type Source interface {
	ReadMsg() (*Message, error)
}

type Sink interface {
	WriteMsg(*Message) error
}

type Conn interface {
	Sink
	Source
	io.Closer
}

func WriteCmd(sink Sink, cmd string, args ...string) error {
	return sink.WriteMsg(&Message{
		Command: cmd,
		Args:    args,
	})
}
