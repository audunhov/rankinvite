package models

import (
	"time"

	"github.com/google/uuid"
)

type Participant struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type StrategyType string

const (
	StrategyPriorityList StrategyType = "priority_list"
	StrategyFCFS         StrategyType = "fcfs"
)

type Strategy struct {
	Type            StrategyType  `json:"type"`
	Participants    []Participant `json:"participants"`
	InviteDuration  time.Duration `json:"invite_duration,omitempty"` // For PriorityList
	TotalDuration   time.Duration `json:"total_duration,omitempty"`  // For FCFS
}

type InvitationStatus string

const (
	StatusDraft  InvitationStatus = "draft"
	StatusActive InvitationStatus = "active"
	StatusClosed InvitationStatus = "closed"
)

type PersonalInviteStatus string

const (
	StatusPending  PersonalInviteStatus = "pending"
	StatusAccepted PersonalInviteStatus = "accepted"
	StatusDeclined PersonalInviteStatus = "declined"
	StatusTimedOut PersonalInviteStatus = "timed_out"
)

type PersonalInvite struct {
	ID               uuid.UUID            `json:"id"`
	ParticipantEmail string               `json:"participant_email"`
	Status           PersonalInviteStatus `json:"status"`
	ExpiresAt        time.Time            `json:"expires_at"`
	ReminderSent     bool                 `json:"reminder_sent"`
}

type Invitation struct {
	ID                   uuid.UUID        `json:"id"`
	Title                string           `json:"title"`
	Location             string           `json:"location"`
	StartTime            time.Time        `json:"start_time"`
	EndTime              time.Time        `json:"end_time"`
	Description          string           `json:"description"`
	CustomEmailTemplate  string           `json:"custom_email_template"`
	Spots                int              `json:"spots"`
	Strategies           []Strategy       `json:"strategies"`
	CurrentStrategyIndex int              `json:"current_strategy_index"`
	Status               InvitationStatus `json:"status"`
	PersonalInvites      []PersonalInvite `json:"personal_invites"`
	CreatedAt            time.Time        `json:"created_at"`
}

func NewInvitation(title string, spots int) *Invitation {
	return &Invitation{
		ID:              uuid.New(),
		Title:           title,
		Spots:           spots,
		Status:          StatusDraft,
		PersonalInvites: []PersonalInvite{},
		CreatedAt:       time.Now(),
	}
}

func (i *Invitation) AcceptedCount() int {
	count := 0
	for _, pi := range i.PersonalInvites {
		if pi.Status == StatusAccepted {
			count++
		}
	}
	return count
}

func (i *Invitation) PendingCount() int {
	count := 0
	for _, pi := range i.PersonalInvites {
		if pi.Status == StatusPending {
			count++
		}
	}
	return count
}
