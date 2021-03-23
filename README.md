# irc

[![godocs.io](https://godocs.io/github.com/hlandau/irc?status.svg)](https://godocs.io/github.com/hlandau/irc) [![No modules](https://www.devever.net/~hl/f/no-modules2.svg) 100% modules-free.](https://www.devever.net/~hl/gomod)

**Don't use this, use [ircproto](https://github.com/hlandau/ircproto) instead.**

This is an IRC client library for Go. The top-level package provides a simple
interface to get started. The interface provided is in the form of `ReadMsg`
and `WriteMsg`. Messages are parsed and serialized for you; see the ircparse
package.

The library is built in layers. The top level package simply composes the basic
services layers implemented in ircbase.

Current layers:

  - Ping handling
  - Registration sequence
  - Auto reconnect
  - Auto channel rejoin

## Licence

    © 2016 Hugo Landau <hlandau@devever.net>    MIT License

