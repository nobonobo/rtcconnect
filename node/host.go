package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/pion/webrtc/v4"

	"github.com/nobonobo/rtcconnect/signaling"
)

// for HostNode
func NewHost(id string) *Node {
	return &Node{
		id:    id,
		peers: map[string]*Node{},
	}
}

func (n *Node) Listen(ctx context.Context) error {
	conn := signaling.New(n.id)
	defer conn.Close()
	ch := conn.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if err := n.handle(ctx, msg); err != nil {
				log.Println(err)
			}
		}
	}
}

func (n *Node) handle(ctx context.Context, msg *signaling.Message) error {
	log.Println("handle:", msg.From, msg.To, msg.Type, string(msg.Payload))
	switch msg.Type {
	case "offer":
		return n.handleOffer(ctx, msg)
	case "candidate":
		return n.handleCandidate(ctx, msg)
	default:
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

func (n *Node) handleOffer(ctx context.Context, msg *signaling.Message) error {
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer}
	if err := json.Unmarshal(msg.Payload, &offer.SDP); err != nil {
		return err
	}
	pc, err := webrtc.NewPeerConnection(signaling.DefaultConfig)
	if err != nil {
		return err
	}
	peer := NewPeer(msg.From, pc)
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		log.Println("connection state changed:", msg.From, pcs)
		switch pcs {
		case webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateFailed:
			peer.Close()
		case webrtc.PeerConnectionStateClosed:
			n.mu.Lock()
			delete(n.peers, msg.From)
			n.mu.Unlock()
		}
	})
	pc.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		log.Println("ice connection state changed:", msg.From, is)
	})
	n.mu.Lock()
	old := n.peers[msg.From]
	n.peers[msg.From] = peer
	n.mu.Unlock()
	if old != nil {
		old.Close()
	}
	pc.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		if peer.DataChannel() == nil {
			b, err := json.Marshal(i.ToJSON())
			if err != nil {
				log.Println(err)
				return
			}
			if err := peer.Publish(ctx, n.id, "candidate", b); err != nil {
				log.Println(err)
			}
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		peer.SetDataChannel(dc)
	})
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	b, err := json.Marshal(answer.SDP)
	if err != nil {
		return err
	}
	log.Println(msg.From, "answer:", string(b))
	if err := peer.Publish(ctx, n.id, "answer", b); err != nil {
		return err
	}
	if n.OnConnected != nil {
		n.OnConnected(peer)
	}
	return nil
}

func (n *Node) handleCandidate(ctx context.Context, msg *signaling.Message) error {
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(msg.Payload, &candidate); err != nil {
		return err
	}
	peer := n.Peer(msg.From)
	if peer == nil {
		return fmt.Errorf("session not found: %s", msg.From)
	}
	if pc := peer.PeerConnection(); pc != nil {
		if err := pc.AddICECandidate(candidate); err != nil {
			return err
		}
	}
	return nil
}

func (n *Node) Broadcast(data []byte) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, peer := range n.peers {
		if err := peer.send(data); err != nil {
			if errors.Is(err, ErrDataChannelNotReady) {
				continue
			}
			return err
		}
	}
	return nil
}
