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
		TotalDuration: 24 * time.Hour,
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
	if inv.Status != StatusCompleted {
		t.Errorf("Expected status completed, got %s", inv.Status)
	}

	// P2 tries to accept (The Race)
	eventsP2 := inv.Handle(Command{Type: CmdAccept, InviteID: inviteIDP2, Now: now.Add(2 * time.Minute)})
	// DistributionPlanCompletedEvent + InvitationClosedEvent
	if len(eventsP2) > 0 && inv.PersonalInvites[1].Status == StatusAccepted {
		t.Errorf("P2 should not have been accepted, but got status %s and %d events", inv.PersonalInvites[1].Status, len(eventsP2))
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
	if len(eventsP1) > 0 && inv.PersonalInvites[0].Status == StatusAccepted {
		t.Error("Should not be accepted after timeout")
	}
}

func TestDSTComplexChain(t *testing.T) {
	// Setup: 2 spots, PriorityList(P1, P2) -> FCFS(P3, P4)
	inv := NewInvitation("Big Event", 2)
	p1 := Participant{Email: "p1@test.com"}
	p2 := Participant{Email: "p2@test.com"}
	p3 := Participant{Email: "p3@test.com"}
	p4 := Participant{Email: "p4@test.com"}

	inv.Strategies = append(inv.Strategies, Strategy{
		Type:           StrategyPriorityList,
		Participants:   []Participant{p1, p2},
		InviteDuration: time.Hour,
	})
	inv.Strategies = append(inv.Strategies, Strategy{
		Type:          StrategyFCFS,
		Participants:  []Participant{p3, p4},
		TotalDuration: 24 * time.Hour,
	})

	now := time.Unix(1000, 0)
	// Start: P1 and P2 invited (since 2 spots available)
	events := inv.Handle(Command{Type: CmdStart, Now: now})
	if len(events) != 2 {
		t.Errorf("Expected 2 initial invites, got %d", len(events))
	}

	// P1 accepts at t+10m
	inv.Handle(Command{Type: CmdAccept, InviteID: inv.PersonalInvites[0].ID, Now: now.Add(10 * time.Minute)})
	if inv.Spots != 1 {
		t.Errorf("Expected 1 spot left, got %d", inv.Spots)
	}

	// P2 declines at t+20m -> should move to next strategy (FCFS) since P2 was the last in PriorityList
	eventsDecline := inv.Handle(Command{Type: CmdDecline, InviteID: inv.PersonalInvites[1].ID, Now: now.Add(20 * time.Minute)})
	
	// Should have invited P3 and P4
	if len(eventsDecline) != 2 {
		t.Errorf("Expected 2 FCFS invites, got %d", len(eventsDecline))
	}
	if inv.CurrentStrategyIndex != 1 {
		t.Errorf("Expected strategy index 1, got %d", inv.CurrentStrategyIndex)
	}

	// P3 accepts at t+30m -> 0 spots left, status closed
	inv.Handle(Command{Type: CmdAccept, InviteID: inv.PersonalInvites[2].ID, Now: now.Add(30 * time.Minute)})
	if inv.Spots != 0 || inv.Status != StatusCompleted {
		t.Errorf("Expected 0 spots and completed status, got %d and %s", inv.Spots, inv.Status)
	}
}

func TestDSTForceNextChain(t *testing.T) {
	inv := NewInvitation("Force Test", 1)
	p1 := Participant{Email: "p1@test.com"}
	p2 := Participant{Email: "p2@test.com"}

	inv.Strategies = append(inv.Strategies, Strategy{
		Type:           StrategyPriorityList,
		Participants:   []Participant{p1},
		InviteDuration: 24 * time.Hour,
	})
	inv.Strategies = append(inv.Strategies, Strategy{
		Type:           StrategyPriorityList,
		Participants:   []Participant{p2},
		InviteDuration: 24 * time.Hour,
	})

	now := time.Unix(1000, 0)
	inv.Handle(Command{Type: CmdStart, Now: now})
	
	if inv.PersonalInvites[0].ParticipantEmail != "p1@test.com" {
		t.Error("P1 should be invited first")
	}

	// Force next -> skip P1, invite P2
	eventsForce := inv.Handle(Command{Type: CmdForceNext, Now: now.Add(time.Minute)})
	if len(eventsForce) != 1 {
		t.Errorf("Expected 1 event for P2, got %d", len(eventsForce))
	}
	if inv.PersonalInvites[0].Status != StatusTimedOut {
		t.Error("P1 should be timed out by force")
	}
	if inv.PersonalInvites[1].ParticipantEmail != "p2@test.com" {
		t.Error("P2 should be invited next")
	}
}
