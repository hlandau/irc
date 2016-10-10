package ircbase

import "github.com/hlandau/irc/ircparse"

type readResponse struct {
	Message *ircparse.Message
	Error   error
}

type writeRequest struct {
	Message    *ircparse.Message
	ResultChan chan error
}
