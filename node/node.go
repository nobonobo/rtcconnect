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
		id: id,
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
		go func() {
			switch pcs {
			case webrtc.PeerConnectionStateConnected:
				done <- nil
			case webrtc.PeerConnectionStateFailed:
				done <- fmt.Errorf("connection failed: %s", pcs)
			}
		}()
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
		go func() {
			outgoing <- i.ToJSON()
		}()
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
								sender.Publish(ctx, n.id, "candidate", b)
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
	sender.Publish(ctx, n.id, "offer", b)
	n.pc = pc
	n.dataChannel = dc
	return <-done
}

func (n *Node) Publish(ctx context.Context, from, kind string, data []byte) error {
	n.conn.Publish(ctx, from, kind, data)
	return nil
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
