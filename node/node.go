package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/nobonobo/rtcconnect/signaling"
)

var (
	ErrDataChannelNotReady = errors.New("data channel not ready")
)

type Node struct {
	mu          sync.RWMutex
	id          string
	conn        *signaling.Signaling
	pc          *webrtc.PeerConnection
	dataChannel *webrtc.DataChannel
	peers       map[string]*Node
	OnConnected func(n *Node)
}

func New(id string) *Node {
	return &Node{
		id:    id,
		peers: map[string]*Node{},
	}
}

func NewPeer(id string, pc *webrtc.PeerConnection) *Node {
	return &Node{
		id:   id,
		conn: signaling.New(id),
		pc:   pc,
	}
}

func (n *Node) Connect(ctx context.Context, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	pc, err := webrtc.NewPeerConnection(signaling.DefaultConfig)
	if err != nil {
		return err
	}
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		log.Println("connection state changed:", pcs)
		switch pcs {
		case webrtc.PeerConnectionStateConnected:
			done <- nil
		case webrtc.PeerConnectionStateFailed:
			done <- fmt.Errorf("connection failed: %s", pcs)
		}
	})
	incoming := make(chan webrtc.ICECandidateInit, 20)
	defer close(incoming)
	outgoing := make(chan webrtc.ICECandidateInit, 20)
	sender := signaling.New(dst)
	defer sender.Close()
	pc.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			close(outgoing)
			return
		}
		outgoing <- i.ToJSON()
	})
	go func() {
		receiver := signaling.New(n.id)
		defer receiver.Close()
		ch := receiver.Subscribe(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if msg.From != dst {
					continue
				}
				switch msg.Type {
				case "answer":
					answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer}
					if err := json.Unmarshal(msg.Payload, &answer.SDP); err != nil {
						log.Println(err)
						continue
					}
					if err := pc.SetRemoteDescription(answer); err != nil {
						log.Println(err)
						continue
					}
					go func() {
						for {
							select {
							case <-ctx.Done():
								return
							case candidate := <-incoming:
								if err := pc.AddICECandidate(candidate); err != nil {
									log.Println(err)
								}
							case candidate := <-outgoing:
								b, err := json.Marshal(candidate)
								if err != nil {
									log.Println(err)
									continue
								}
								if err := sender.Publish(n.id, "candidate", b); err != nil {
									log.Println(err)
								}
							}
						}
					}()
				case "candidate":
					var candidate webrtc.ICECandidateInit
					if err := json.Unmarshal(msg.Payload, &candidate); err != nil {
						log.Println(err)
						continue
					}
					incoming <- candidate
				default:
					log.Printf("unknown message type: %s", msg.Type)
				}
			}
		}
	}()
	dc, err := pc.CreateDataChannel("data", nil)
	if err != nil {
		return err
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	b, err := json.Marshal(offer.SDP)
	if err != nil {
		return err
	}
	log.Println("send offer", dst, "/", string(b))
	if err := sender.Publish(n.id, "offer", b); err != nil {
		return err
	}
	n.pc = pc
	n.dataChannel = dc
	return <-done
}

func (n *Node) Publish(from, kind string, data []byte) error {
	return n.conn.Publish(from, kind, data)
}

func (n *Node) Peer(id string) *Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.peers[id]
}

func (n *Node) clear() {
	for _, peer := range n.peers {
		peer.Close()
	}
	n.peers = map[string]*Node{}
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clear()
	if n.dataChannel != nil {
		n.dataChannel.Close()
		n.dataChannel = nil
	}
	if n.pc != nil {
		n.pc.Close()
		n.pc = nil
	}
	return nil
}

func (n *Node) ID() string {
	return n.id
}

func (n *Node) PeerConnection() *webrtc.PeerConnection {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.pc
}

func (n *Node) SetDataChannel(dc *webrtc.DataChannel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dataChannel = dc
}

func (n *Node) DataChannel() *webrtc.DataChannel {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.dataChannel
}

func (n *Node) send(data []byte) error {
	if n.dataChannel == nil {
		return ErrDataChannelNotReady
	}
	return n.dataChannel.Send(data)
}

func (n *Node) Send(data []byte) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.send(data)
}

// for HostNode

func (n *Node) Listen(ctx context.Context) error {
	conn := signaling.New(n.id)
	defer conn.Close()
	defer n.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := conn.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if err := n.handle(msg); err != nil {
				return err
			}
		}
	}
}

func (n *Node) handle(msg *signaling.Message) error {
	log.Println("handle:", msg.From, msg.To, msg.Type, string(msg.Payload))
	switch msg.Type {
	case "offer":
		return n.handleOffer(msg)
	case "candidate":
		return n.handleCandidate(msg)
	default:
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

func (n *Node) handleOffer(msg *signaling.Message) error {
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
			if err := peer.Publish(n.id, "candidate", b); err != nil {
				log.Println(err)
			}
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		peer.SetDataChannel(dc)
		dc.OnOpen(func() {
			log.Println("data channel opened:", msg.From)
		})
		dc.OnClose(func() {
			log.Println("data channel closed:", msg.From)
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			log.Println("received:", string(msg.Data))
		})
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
	if err := peer.Publish(n.id, "answer", b); err != nil {
		return err
	}
	if n.OnConnected != nil {
		n.OnConnected(peer)
	}
	return nil
}

func (n *Node) handleCandidate(msg *signaling.Message) error {
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(msg.Payload, &candidate); err != nil {
		return err
	}
	peer := n.Peer(msg.From)
	if peer == nil {
		return fmt.Errorf("session not found: %s", msg.From)
	}
	if err := peer.PeerConnection().AddICECandidate(candidate); err != nil {
		return err
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
