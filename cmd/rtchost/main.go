package main

import (
	"context"
	"flag"
	"log"

	"github.com/nobonobo/rtcconnect/node"
	"github.com/pion/webrtc/v4"
)

func main() {
	log.SetFlags(log.Lshortfile)
	id := "host-sample1"
	flag.StringVar(&id, "id", id, "host id")
	flag.Parse()
	host := node.NewHost(id)
	defer host.Close()
	host.OnConnected = func(n *node.Node) {
		log.Printf("Connected: %s", n.ID())
		n.PeerConnection().OnDataChannel(func(dc *webrtc.DataChannel) {
			log.Println("dc:", dc)
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				log.Println(string(msg.Data))
			})
			dc.OnOpen(func() {
				log.Println("data channel opened:", dc.Label())
			})
			dc.OnClose(func() {
				log.Println("data channel closed:", dc.Label())
			})
		})
	}
	defer host.Close()
	ctx := context.Background()
	log.Println("host started:", id)
	if err := host.Listen(ctx); err != nil {
		log.Fatal(err)
	}
}
