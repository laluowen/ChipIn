package socket

import (
	"io"
	"log"
	"testing"

	"github.com/slack-go/slack/socketmode"
)

func TestEnqueueEventIsBoundedAndAcknowledgesOnlyAcceptedEvents(t *testing.T) {
	events := make(chan socketmode.Event, 1)
	logger := log.New(io.Discard, "", 0)
	first := socketmode.Event{Type: socketmode.EventTypeSlashCommand}
	acks := 0
	ack := func() {
		if len(events) != 0 {
			t.Error("event was queued before acknowledgement")
		}
		acks++
	}

	if !enqueueEvent(events, first, ack, logger) {
		t.Fatal("first event was rejected")
	}
	if enqueueEvent(events, socketmode.Event{Type: socketmode.EventTypeInteractive}, ack, logger) {
		t.Fatal("event was accepted when the queue was full")
	}
	if acks != 1 {
		t.Fatalf("got %d acknowledgements, want 1", acks)
	}
	if len(events) != 1 {
		t.Fatalf("got %d queued events, want 1", len(events))
	}
	if got := <-events; got.Type != first.Type {
		t.Fatalf("got queued event type %q, want %q", got.Type, first.Type)
	}
}
