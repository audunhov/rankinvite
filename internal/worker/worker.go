package worker

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"rankinvite/internal/models"
	"rankinvite/internal/storage"
	"time"
)

type Worker struct {
	repo    *storage.InvitationRepository
	baseURL string
}

func NewWorker(repo *storage.InvitationRepository) *Worker {
	return &Worker{repo: repo}
}

func (w *Worker) SetBaseURL(u string) {
	w.baseURL = u
}

func (w *Worker) Start() {
	slog.Info("Starting background worker", "tick_interval", "10s")
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			w.tick()
		}
	}()
}

func (w *Worker) ProcessEvents(events []models.Event) {
	if len(events) > 0 {
		slog.Debug("Processing events", "count", len(events))
	}
	for _, event := range events {
		w.processEvent(event)
	}
}

func (w *Worker) tick() {
	invs, err := w.repo.List(1000, 0)
	if err != nil {
		slog.Error("Worker failed to list invitations", "error", err)
		return
	}

	now := time.Now()
	for _, inv := range invs {
		if inv.Status != models.StatusActive {
			continue
		}

		slog.Debug("Ticking invitation", "id", inv.ID, "title", inv.Title)
		events := inv.Handle(models.Command{
			Type:    models.CmdTick,
			Now:     now,
			BaseURL: w.baseURL,
		})

		if len(events) > 0 {
			slog.Info("Invitation state changed during tick", "id", inv.ID, "events", len(events))
			if err := w.repo.Save(inv); err != nil {
				slog.Error("Failed to save invitation after tick", "id", inv.ID, "error", err)
				continue
			}
			w.ProcessEvents(events)
		}
	}
}

func (w *Worker) processEvent(event models.Event) {
	switch e := event.(type) {
	case models.EmailSentEvent:
		w.sendEmail(e)
	case models.InvitationClosedEvent:
		slog.Info("Invitation closed", "reason", e.Reason)
	case models.SpotFilledEvent:
		slog.Info("Spot filled", "email", e.ParticipantEmail, "remaining", e.RemainingSpots)
	default:
		slog.Warn("Unknown event type encountered", "type", fmt.Sprintf("%T", event))
	}
}

func (w *Worker) sendEmail(e models.EmailSentEvent) {
	slog.Info("Attempting to send email", "recipient", e.Recipient, "subject", e.Subject)
	
	// MailHog default: localhost:1025
	from := "system@rankinvite.no"
	to := []string{e.Recipient}
	
	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"\r\n"+
		"Hei! Du er herved invitert. Klikk her for å svare: %s\r\n", e.Recipient, e.Subject, e.URL))

	err := smtp.SendMail("localhost:1025", nil, from, to, msg)
	if err != nil {
		slog.Error("Failed to send email", "recipient", e.Recipient, "error", err)
	} else {
		slog.Info("Email sent successfully", "recipient", e.Recipient)
	}
}
