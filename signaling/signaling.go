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

type Signaling struct {
	id       string
	operator *operator.Operator
}

func New(id string) *Signaling {
	s := &Signaling{
		id:       id,
		operator: operator.New(),
	}
	return s
}

func (s *Signaling) Close() error {
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

func (s *Signaling) Publish(ctx context.Context, from, kind string, payload json.RawMessage) error {
	b, err := json.Marshal(&Message{
		From:    from,
		To:      s.id,
		Type:    kind,
		Payload: payload,
	})
	if err != nil {
		return err
	}
	return s.operator.Pub(ctx, s.id, b)
}
