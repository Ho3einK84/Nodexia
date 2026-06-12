package terminalticket_test

import (
	"testing"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/terminalticket"
)

func req() sshclient.ConnectionRequest {
	return sshclient.ConnectionRequest{Host: "10.0.0.1", Port: 22, Username: "root"}
}

func TestCreateAndConsume(t *testing.T) {
	s := terminalticket.New(30 * time.Second)

	id := s.Create(1, req())
	if id == "" {
		t.Fatal("Create returned empty id")
	}

	ticket, ok := s.Consume(id)
	if !ok {
		t.Fatal("Consume returned false for valid ticket")
	}
	if ticket.ID != id {
		t.Errorf("ticket.ID = %q, want %q", ticket.ID, id)
	}
	if ticket.ServerID != 1 {
		t.Errorf("ticket.ServerID = %d, want 1", ticket.ServerID)
	}
}

func TestConsumeSingleUse(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	id := s.Create(1, req())

	if _, ok := s.Consume(id); !ok {
		t.Fatal("first Consume returned false")
	}
	if _, ok := s.Consume(id); ok {
		t.Error("second Consume returned true — expected single-use rejection")
	}
}

func TestConsumeExpired(t *testing.T) {
	// TTL of 1 ms so the ticket is already expired.
	s := terminalticket.New(1 * time.Millisecond)
	id := s.Create(1, req())
	time.Sleep(5 * time.Millisecond)

	if _, ok := s.Consume(id); ok {
		t.Error("Consume returned true for expired ticket")
	}
}

func TestConsumeUnknown(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	if _, ok := s.Consume("does-not-exist"); ok {
		t.Error("Consume returned true for unknown id")
	}
}

func TestRelease(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	id := s.Create(1, req())
	s.Consume(id)
	s.Release(id)

	// A second Consume must still fail after Release.
	if _, ok := s.Consume(id); ok {
		t.Error("Consume succeeded after Release")
	}
}

func TestSessionLimitFourthRejected(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	const max = 3

	for i := 0; i < max; i++ {
		if !s.TryAcquireSession("alice", max) {
			t.Fatalf("TryAcquireSession failed on attempt %d", i+1)
		}
	}

	if s.TryAcquireSession("alice", max) {
		t.Error("4th TryAcquireSession should have been rejected")
	}
}

func TestSessionLimitReleasedAllowsNext(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	const max = 3

	for i := 0; i < max; i++ {
		s.TryAcquireSession("alice", max)
	}
	s.ReleaseSession("alice")

	if !s.TryAcquireSession("alice", max) {
		t.Error("TryAcquireSession after ReleaseSession should succeed")
	}
}

func TestSessionLimitIsolatedPerUser(t *testing.T) {
	s := terminalticket.New(30 * time.Second)
	const max = 3

	// Fill alice's quota.
	for i := 0; i < max; i++ {
		s.TryAcquireSession("alice", max)
	}

	// Bob should be unaffected.
	if !s.TryAcquireSession("bob", max) {
		t.Error("bob should not be limited by alice's session count")
	}
}
