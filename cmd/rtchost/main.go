package main

import (
	"context"
	"flag"
	"log"

	"github.com/nobonobo/rtcconnect/node"
)

func main() {
	log.SetFlags(log.Lshortfile)
	id := "host-sample1"
	flag.StringVar(&id, "id", id, "host id")
	flag.Parse()
	ctx := context.Background()
	h := node.New(id)
	defer h.Close()
	log.Println("host started:", id)
	if err := h.Listen(ctx); err != nil {
		log.Fatal(err)
	}
}
