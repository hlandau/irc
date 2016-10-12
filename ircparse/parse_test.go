package ircparse

import "testing"
import "reflect"

type item struct {
	s  string
	ok bool
	m  []*Message
}

var ss = []item{
	item{"PING\r\n", true, []*Message{
		{
			Command: "PING",
		},
	}},
	item{"PING foo\r\n", true, []*Message{
		{
			Command: "PING",
			Args:    []string{"foo"},
		},
	}},
	item{"PING foo bar\r\n", true, []*Message{
		{
			Command: "PING",
			Args:    []string{"foo", "bar"},
		},
	}},
	item{"PING foo bar baz\r\n", true, []*Message{
		{
			Command: "PING",
			Args:    []string{"foo", "bar", "baz"},
		},
	}},
	item{"PING foo bar baz :alpha beta gamma delta\r\n", true, []*Message{
		{
			Command: "PING",
			Args:    []string{"foo", "bar", "baz", "alpha beta gamma delta"},
		},
	}},
	item{"043 okay well :this is a numeric\r\n", true, []*Message{
		{
			Command: "043",
			Args:    []string{"okay", "well", "this is a numeric"},
		},
	}},
	item{"01 bad numeric\r\n", false, []*Message{}},
	item{"\r\n", false, []*Message{}},
	item{":server PING\r\n", true, []*Message{
		{
			ServerName: "server",
			Command:    "PING",
		},
	}},
	item{":server.name PING\r\n", true, []*Message{
		{
			ServerName: "server.name",
			Command:    "PING",
		},
	}},
	item{":the.server-name PING\r\n", true, []*Message{
		{
			ServerName: "the.server-name",
			Command:    "PING",
		},
	}},
	item{":the.server-name. PING\r\n", true, []*Message{
		{
			ServerName: "the.server-name",
			Command:    "PING",
		},
	}},
	item{":nick!user@host PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "host",
			Command:  "PING",
		},
	}},
	item{":nick!user@host.name PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "host.name",
			Command:  "PING",
		},
	}},
	item{":nick!user@111.222.101.104 PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "111.222.101.104",
			Command:  "PING",
		},
	}},
	// Charybdis can mangle IP addresses to use non-hexadecimal letters; we must accept this
	item{":nick!user@111.222.xyz.xWz PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "111.222.xyz.xWz",
			Command:  "PING",
		},
	}},
	item{":nick!user@dead:beef:dead:beef:1234:4567:89ad:beef PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "dead:beef:dead:beef:1234:4567:89ad:beef",
			Command:  "PING",
		},
	}},
	// Charybdis can mangle IP addresses to use non-hexadecimal letters; we must accept this
	item{":nick!user@dead:bxyz:dXYZ:beef:1234:z567:89ad:Zeef PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "dead:bxyz:dXYZ:beef:1234:z567:89ad:Zeef",
			Command:  "PING",
		},
	}},
	item{":nick!user@zead:bxyz:dXYZ:beef:1234:z567:89ad:Zeef PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "zead:bxyz:dXYZ:beef:1234:z567:89ad:Zeef",
			Command:  "PING",
		},
	}},
	// Freenode can use slashes in vhosts
	item{":nick!user@this/is/my/Host PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "user",
			HostName: "this/is/my/Host",
			Command:  "PING",
		},
	}},
	item{":nick!~user@host.name PING\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "~user",
			HostName: "host.name",
			Command:  "PING",
		},
	}},
	item{"@alpha :nick!~user@host.name PING foo bar :this is message\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "~user",
			HostName: "host.name",
			Command:  "PING",
			Args:     []string{"foo", "bar", "this is message"},
			Tags: map[string]string{
				"alpha": "",
			},
		},
	}},
	item{"@alpha=beta :nick!~user@host.name PING foo bar :this is message\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "~user",
			HostName: "host.name",
			Command:  "PING",
			Args:     []string{"foo", "bar", "this is message"},
			Tags: map[string]string{
				"alpha": "beta",
			},
		},
	}},
	item{"@example.com/alpha=beta :nick!~user@host.name PING foo bar :this is message\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "~user",
			HostName: "host.name",
			Command:  "PING",
			Args:     []string{"foo", "bar", "this is message"},
			Tags: map[string]string{
				"example.com/alpha": "beta",
			},
		},
	}},
	item{"@example.com/alpha=beta;foo=bar;baz=1 :nick!~user@host.name PING foo bar :this is message\r\n", true, []*Message{
		{
			NickName: "nick",
			UserName: "~user",
			HostName: "host.name",
			Command:  "PING",
			Args:     []string{"foo", "bar", "this is message"},
			Tags: map[string]string{
				"example.com/alpha": "beta",
				"foo":               "bar",
				"baz":               "1",
			},
		},
	}},
}

func TestParse(t *testing.T) {
	for _, i := range ss {
		p := &Parser{}
		p.Parse(i.s)
		if i.ok != (p.MalformedMessageCount == 0) {
			t.Errorf("test was supposed to return %s but didn't: %s", i.ok, i.s)
		}
		if p.MalformedMessageCount == 0 {
			msgs := p.PopMessages()
			if len(msgs) != 1 {
				t.Errorf("returned wrong number of messages: %s", len(msgs))
			}

			if !reflect.DeepEqual(msgs, i.m) {
				t.Errorf("mismatch: %#v != %#v", msgs, i.m)
				for _, v := range msgs {
					t.Errorf("  a: %#v", v)
				}
				for _, v := range i.m {
					t.Errorf("  b: %#v", v)
				}
			}

			/*if len(msgs) > 0 {
			  s := msgs[0].String()
			  if s != i.s {
			    t.Errorf("sz mismatch: %#v != %#v", s, i.s)
			  }
			}*/
		}
	}
}
