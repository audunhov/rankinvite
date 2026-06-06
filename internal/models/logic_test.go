package models

import (
	"testing"
	"time"
)

func TestDSTRaceConditionLastSpot(t *testing.T) {
	inv := NewInvitation("Debate Night", 1)
	p1 := Participant{Email: "p1@test.com"}
	p2 := Participant{Email: "p2@test.com"}

	inv.Strategies = append(inv.Strategies, Strategy{
		Type:         StrategyFCFS,
		Participants: []Participant{p1, p2},
	})

	now := time.Unix(1000, 0)
	events := inv.Handle(Command{Type: CmdStart, Now: now})
	if len(events) != 2 {
		t.Errorf("Expected 2 email events, got %d", len(events))
	}

	inviteIDP1 := inv.PersonalInvites[0].ID
	inviteIDP2 := inv.PersonalInvites[1].ID

	// P1 accepts
	inv.Handle(Command{Type: CmdAccept, InviteID: inviteIDP1, Now: now.Add(time.Minute)})
	if inv.Spots != 0 {
		t.Errorf("Expected 0 spots, got %d", inv.Spots)
	}
	if inv.Status != StatusClosed {
		t.Errorf("Expected status closed, got %s", inv.Status)
	}

	// P2 tries to accept (The Race)
	eventsP2 := inv.Handle(Command{Type: CmdAccept, InviteID: inviteIDP2, Now: now.Add(2 * time.Minute)})
	if len(eventsP2) > 0 {
		t.Errorf("Expected no events for late accept, got %d", len(eventsP2))
	}
	if inv.PersonalInvites[1].Status == StatusAccepted {
		t.Error("P2 should not have been accepted")
	}
}

func TestDSTLateAcceptanceAfterTimeout(t *testing.T) {
	inv := NewInvitation("Meeting", 1)
	p1 := Participant{Email: "p1@test.com"}
	p2 := Participant{Email: "p2@test.com"}

	inv.Strategies = append(inv.Strategies, Strategy{
		Type:           StrategyPriorityList,
		Participants:   []Participant{p1, p2},
		InviteDuration: 2 * time.Hour,
	})

	now := time.Unix(1000, 0)
	inv.Handle(Command{Type: CmdStart, Now: now})
	inviteIDP1 := inv.PersonalInvites[0].ID

	// Time passes past timeout
	inv.Handle(Command{Type: CmdTick, Now: now.Add(3 * time.Hour)})

	if inv.PersonalInvites[0].Status != StatusTimedOut {
		t.Errorf("P1 should be timed out, got %s", inv.PersonalInvites[0].Status)
	}

	// P1 tries to accept late
	eventsP1 := inv.Handle(Command{Type: CmdAccept, InviteID: inviteIDP1, Now: now.Add(4 * time.Hour)})
	if len(eventsP1) > 0 {
		t.Error("Should not produce events for late accept")
	}
}
