package signaling

import (
	"context"
	"encoding/json"
	"log"

	"github.com/nobonobo/operator"
	"github.com/pion/webrtc/v4"
)

var DefaultConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	},
}

type Message struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type entry struct {
	ctx context.Context
	b   []byte
}
type Signaling struct {
	id       string
	operator *operator.Operator
	queue    chan entry
}

func New(id string) *Signaling {
	s := &Signaling{
		id:       id,
		operator: operator.New(),
		queue:    make(chan entry, 1),
	}
	go s.do()
	return s
}

func (s *Signaling) Close() error {
	close(s.queue)
	return nil
}

func (s *Signaling) Subscribe(ctx context.Context) <-chan *Message {
	ch := make(chan *Message, 1)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			b, err := s.operator.Sub(ctx, s.id)
			if err != nil {
				log.Println(err)
				continue
			}
			var msg *Message
			if err := json.Unmarshal(b, &msg); err != nil {
				log.Println(err)
				continue
			}
			ch <- msg
		}
	}()
	return ch
}

func (s *Signaling) do() {
	for v := range s.queue {
		select {
		case <-v.ctx.Done():
			continue
		default:
			if err := s.operator.Pub(v.ctx, s.id, v.b); err != nil {
				log.Println(err)
			}
		}
	}
}

func (s *Signaling) Publish(ctx context.Context, from, kind string, payload json.RawMessage) {
	go func() {
		b, _ := json.Marshal(&Message{
			From:    from,
			To:      s.id,
			Type:    kind,
			Payload: payload,
		})
		select {
		case <-ctx.Done():
			return
		case s.queue <- entry{ctx, b}:
		}
	}()
}
