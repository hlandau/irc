package main

import (
	"crypto/tls"
	"github.com/hlandau/dexlogconfig"
	"github.com/hlandau/irc"
	"github.com/hlandau/irc/ircbase"
	"github.com/hlandau/irc/ircdial"
	"github.com/hlandau/irc/ircparse"
	"github.com/hlandau/xlog"
	"gopkg.in/hlandau/easyconfig.v1"
)

var log, Log = xlog.New("examplebot")

func main() {
	config := easyconfig.Configurator{}
	config.ParseFatal(nil)
	dexlogconfig.Init()

	conn, err := irc.Dial("tls", "127.0.0.1:6697", irc.Config{
		Dial: ircdial.Config{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Register: ircbase.RegisterConfig{
			NickName: "xbot",
		},
	})
	log.Fatale(err, "conn")

	defer conn.Close()

	err = ircparse.WriteCmd(conn, "JOIN", "#btest")
	log.Errore(err, "join")

	for {
		_, err := conn.ReadMsg()
		log.Fatale(err, "read msg")
	}
}
