package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/nobonobo/rtcconnect/node"
)

func main() {
	log.SetFlags(log.Lshortfile)
	id, host := "node1", "host-sample1"
	flag.StringVar(&id, "id", id, "node id")
	flag.StringVar(&host, "host", host, "host id")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := node.New(id)
	defer n.Close()
	if err := n.Connect(ctx, host); err != nil {
		log.Fatal(err)
	}
	n.PeerConnection().OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Println("connection state changed:", state)
		switch state {
		case webrtc.PeerConnectionStateConnected:
		case webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateFailed:
			cancel()
		}
	})
	n.DataChannel().OnOpen(func() {
		log.Println("data channel opened:", host)
		go func() {
			tick := time.NewTicker(time.Second)
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if err := n.DataChannel().Send([]byte(id + " : hello")); err != nil {
						log.Println(err)
					}
				}
			}
		}()
	})
	n.DataChannel().OnClose(func() {
		log.Println("data channel closed:", host)
	})
	n.DataChannel().OnMessage(func(msg webrtc.DataChannelMessage) {
		log.Println("received:", string(msg.Data))
	})
	log.Println("node started:", id)
	<-ctx.Done()
}
