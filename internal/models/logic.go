package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Event interface{}

type EmailSentEvent struct {
	Recipient string
	Subject   string
	URL       string
}

type InvitationClosedEvent struct {
	Reason string
}

type SpotFilledEvent struct {
	ParticipantEmail string
	RemainingSpots   int
}

type CommandType string

const (
	CmdStart  CommandType = "start"
	CmdAccept CommandType = "accept"
	CmdDecline CommandType = "decline"
	CmdTick    CommandType = "tick"
	CmdForceNext CommandType = "force_next"
	CmdCancel  CommandType = "cancel"
)

type Command struct {
	Type     CommandType
	InviteID uuid.UUID // Only for Accept/Decline
	Now      time.Time
	BaseURL  string // e.g. "http://localhost:8080"
}

func (i *Invitation) Handle(cmd Command) []Event {
	var events []Event

	switch cmd.Type {
	case CmdStart:
		if i.Status == StatusDraft && len(i.Strategies) > 0 {
			i.Status = StatusActive
			i.CreatedAt = cmd.Now
			events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
		}

	case CmdAccept:
		if i.Status != StatusActive {
			return events
		}
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.ID == cmd.InviteID && invite.Status == StatusPending {
				if cmd.Now.Before(invite.ExpiresAt) && i.Spots > 0 {
					invite.Status = StatusAccepted
					i.Spots--
					events = append(events, SpotFilledEvent{
						ParticipantEmail: invite.ParticipantEmail,
						RemainingSpots:   i.Spots,
					})

					if i.Spots == 0 {
						i.Status = StatusClosed
						events = append(events, InvitationClosedEvent{Reason: "All spots filled"})
						// Invalidate others
						for j := range i.PersonalInvites {
							if i.PersonalInvites[j].Status == StatusPending {
								i.PersonalInvites[j].Status = StatusTimedOut
							}
						}
					} else {
						events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
					}
				} else {
					invite.Status = StatusTimedOut
				}
				break
			}
		}

	case CmdDecline:
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.ID == cmd.InviteID && invite.Status == StatusPending {
				invite.Status = StatusDeclined
				events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
				break
			}
		}

	case CmdTick:
		if i.Status != StatusActive {
			return events
		}
		changed := false
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.Status == StatusPending && cmd.Now.After(invite.ExpiresAt) {
				invite.Status = StatusTimedOut
				changed = true
			}
		}
		if changed {
			events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
		}

	case CmdForceNext:
		if i.Status != StatusActive {
			return events
		}
		// Force timeout all current pending invites
		for idx := range i.PersonalInvites {
			if i.PersonalInvites[idx].Status == StatusPending {
				i.PersonalInvites[idx].Status = StatusTimedOut
			}
		}
		// Try to activate next steps
		events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)

	case CmdCancel:
		i.Status = StatusClosed
		for idx := range i.PersonalInvites {
			if i.PersonalInvites[idx].Status == StatusPending {
				i.PersonalInvites[idx].Status = StatusTimedOut
			}
		}
		events = append(events, InvitationClosedEvent{Reason: "Cancelled by administrator"})
	}

	return events
}

func (i *Invitation) activateCurrentStrategy(now time.Time, baseURL string) []Event {
	var events []Event

	if i.Status != StatusActive || i.Spots == 0 {
		return events
	}

	if i.CurrentStrategyIndex >= len(i.Strategies) {
		i.Status = StatusClosed
		events = append(events, InvitationClosedEvent{Reason: "All strategies exhausted"})
		return events
	}

	strategy := i.Strategies[i.CurrentStrategyIndex]

	switch strategy.Type {
	case StrategyPriorityList:
		activeCount := 0
		for _, inv := range i.PersonalInvites {
			if inv.Status == StatusPending {
				activeCount++
			}
		}

		toInvite := i.Spots - activeCount
		if toInvite <= 0 {
			return events
		}

		for _, participant := range strategy.Participants {
			if toInvite == 0 {
				break
			}

			alreadyInvited := false
			for _, inv := range i.PersonalInvites {
				if inv.ParticipantEmail == participant.Email {
					alreadyInvited = true
					break
				}
			}

			if !alreadyInvited {
				inviteID := uuid.New()
				expiresAt := now.Add(strategy.InviteDuration)
				i.PersonalInvites = append(i.PersonalInvites, PersonalInvite{
					ID:               inviteID,
					ParticipantEmail: participant.Email,
					Status:           StatusPending,
					ExpiresAt:        expiresAt,
				})
				events = append(events, EmailSentEvent{
					Recipient: participant.Email,
					Subject:   fmt.Sprintf("Invitation: %s", i.Title),
					URL:       fmt.Sprintf("%s/i/%s", baseURL, inviteID),
				})
				toInvite--
			}
		}

		// Move to next strategy if nothing is pending and we have more spots
		stillPending := false
		for _, inv := range i.PersonalInvites {
			if inv.Status == StatusPending {
				stillPending = true
				break
			}
		}
		if !stillPending && toInvite > 0 {
			i.CurrentStrategyIndex++
			events = append(events, i.activateCurrentStrategy(now, baseURL)...)
		}

	case StrategyFCFS:
		sentAny := false
		for _, participant := range strategy.Participants {
			alreadyInvited := false
			for _, inv := range i.PersonalInvites {
				if inv.ParticipantEmail == participant.Email {
					alreadyInvited = true
					break
				}
			}

			if !alreadyInvited {
				inviteID := uuid.New()
				// Use TotalDuration for FCFS as requested
				expiresAt := now.Add(strategy.TotalDuration)
				if strategy.TotalDuration == 0 {
					expiresAt = now.Add(365 * 24 * time.Hour) // Default to long if not set
				}
				
				i.PersonalInvites = append(i.PersonalInvites, PersonalInvite{
					ID:               inviteID,
					ParticipantEmail: participant.Email,
					Status:           StatusPending,
					ExpiresAt:        expiresAt,
				})
				events = append(events, EmailSentEvent{
					Recipient: participant.Email,
					Subject:   fmt.Sprintf("Invitation: %s", i.Title),
					URL:       fmt.Sprintf("%s/i/%s", baseURL, inviteID),
				})
				sentAny = true
			}
		}

		// If we didn't send anything new, check if we should move to next strategy
		if !sentAny {
			stillPending := false
			for _, inv := range i.PersonalInvites {
				if inv.Status == StatusPending {
					stillPending = true
					break
				}
			}
			if !stillPending {
				i.CurrentStrategyIndex++
				events = append(events, i.activateCurrentStrategy(now, baseURL)...)
			}
		}
	}

	return events
}
