package stream

import (
	"sync"

	"github.com/handziurdmytro/skelar/internal/support/ports"
)

// Broker is an in-memory pub/sub keyed by ticket id. A slow subscriber
// drops events rather than blocking the pipeline — it catches up from the
// database via After() on reconnect with Last-Event-ID.
type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan ports.Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: map[string]map[chan ports.Event]struct{}{}}
}

func (b *Broker) Subscribe(ticketID string) (<-chan ports.Event, func()) {
	ch := make(chan ports.Event, 64)
	b.mu.Lock()
	if b.subs[ticketID] == nil {
		b.subs[ticketID] = map[chan ports.Event]struct{}{}
	}
	b.subs[ticketID][ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.subs[ticketID], ch)
		if len(b.subs[ticketID]) == 0 {
			delete(b.subs, ticketID)
		}
		b.mu.Unlock()
	}
}

func (b *Broker) Publish(e ports.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[e.TicketID] {
		select {
		case ch <- e:
		default:
		}
	}
}
