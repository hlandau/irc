package ircparse

import "io"

// Source of IRC messages. Must be thread-safe.
type Source interface {
	ReadMsg() (*Message, error)
}

// Sink for IRC messages. Must be thread-safe.
type Sink interface {
	WriteMsg(*Message) error
}

// Combines a Source, Sink and io.Closer. Must be thread-safe.
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
