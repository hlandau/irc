package ircparse

type MessageSink interface {
	WriteMsg(*Message) error
}

func WriteCmd(sink MessageSink, cmd string, args ...string) error {
	return sink.WriteMsg(&Message{
		Command: cmd,
		Args:    args,
	})
}
