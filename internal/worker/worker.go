package worker

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"rankinvite/internal/auth"
	"rankinvite/internal/models"
	"rankinvite/internal/storage"
	"time"
)

type Worker struct {
	repo    *storage.InvitationRepository
	auth    *auth.AuthService
	baseURL string
}

func NewWorker(repo *storage.InvitationRepository, authService *auth.AuthService) *Worker {
	return &Worker{
		repo: repo,
		auth: authService,
	}
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

func (w *Worker) ProcessEvents(events []models.Event, inv *models.Invitation) {
	if len(events) > 0 {
		slog.Debug("Processing events", "count", len(events))
	}
	for _, event := range events {
		w.processEvent(event, inv)
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
			w.ProcessEvents(events, inv)
		}
	}
}

func (w *Worker) processEvent(event models.Event, inv *models.Invitation) {
	adminURL := fmt.Sprintf("%s/admin/invitations/%s", w.baseURL, inv.ID)
	
	details := ""
	if inv != nil {
		if inv.Location != "" {
			details += "\n\nSTED: " + inv.Location
		}
		if !inv.StartTime.IsZero() {
			details += "\nTID: " + inv.StartTime.Format("02.01.2006 kl. 15:04")
		}
	}

	switch e := event.(type) {
	case models.EmailSentEvent:
		w.sendEmail(e, inv)
	case models.ReminderEmailSentEvent:
		w.sendEmail(models.EmailSentEvent(e), inv)
	case models.InvitationClosedEvent:
		slog.Info("Invitation closed", "reason", e.Reason)
	case models.InvitationFullyBookedEvent:
		body := fmt.Sprintf("Alle plasser til arrangementet '%s' er nå fylt opp.%s", e.Title, details)
		w.notifyAdmins(e.Subscribers, 
			fmt.Sprintf("FULLBOOKET: %s", e.Title),
			body, adminURL, "SE DETALJER I DASHBOARD")
	case models.DistributionPlanCompletedEvent:
		body := fmt.Sprintf("Alle deltakere i utsendelsesplanen for '%s' har blitt invitert og svarfristen har utløpt. Det er fortsatt %d plasser ledig.%s", e.Title, e.RemainingSpots, details)
		w.notifyAdmins(e.Subscribers,
			fmt.Sprintf("PLAN FULLFØRT: %s", e.Title),
			body, adminURL, "SE DETALJER I DASHBOARD")
	case models.SpotFilledEvent:
		slog.Info("Spot filled", "email", e.ParticipantEmail, "remaining", e.RemainingSpots)
	default:
		slog.Warn("Unknown event type encountered", "type", fmt.Sprintf("%T", event))
	}
}

func (w *Worker) notifyAdmins(subscribers []string, subject, body, url, buttonText string) {
	if len(subscribers) == 0 {
		return
	}

	smtpHost, _ := w.repo.GetSetting("smtp_host")
	smtpPort, _ := w.repo.GetSetting("smtp_port")
	smtpUser, _ := w.repo.GetSetting("smtp_user")
	smtpPass, _ := w.repo.GetSetting("smtp_pass")
	globalSenderEmail, _ := w.repo.GetSetting("global_sender_email")

	htmlBody := models.RenderGenericEmail("RANKINVITE VARSEL", body, url, buttonText)

	for _, recipient := range subscribers {
		from := globalSenderEmail
		to := []string{recipient}
		
		header := make(map[string]string)
		header["From"] = from
		header["To"] = recipient
		header["Subject"] = subject
		header["MIME-Version"] = "1.0"
		header["Content-Type"] = "text/html; charset=\"utf-8\""

		message := ""
		for k, v := range header {
			message += fmt.Sprintf("%s: %s\r\n", k, v)
		}
		message += "\r\n" + htmlBody

		var auth smtp.Auth
		if smtpUser != "" {
			auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
		}

		err := smtp.SendMail(smtpHost+":"+smtpPort, auth, from, to, []byte(message))
		if err != nil {
			slog.Warn("Failed to notify admin", "admin", recipient, "error", err)
		} else {
			slog.Info("Admin notified", "admin", recipient)
		}
	}
}

func (w *Worker) sendEmail(e models.EmailSentEvent, inv *models.Invitation) {
	slog.Info("Attempting to send email", "recipient", e.Recipient, "subject", e.Subject)

	smtpHost, _ := w.repo.GetSetting("smtp_host")
	smtpPort, _ := w.repo.GetSetting("smtp_port")
	smtpUser, _ := w.repo.GetSetting("smtp_user")
	smtpPass, _ := w.repo.GetSetting("smtp_pass")

	fromName := inv.SenderName
	fromEmail := inv.SenderEmail

	if fromEmail == "" {
		// Fallback to global settings
		fromName, _ = w.repo.GetSetting("global_sender_name")
		fromEmail, _ = w.repo.GetSetting("global_sender_email")
	}

	from := fmt.Sprintf("%s <%s>", fromName, fromEmail)
	to := []string{e.Recipient}

	header := make(map[string]string)
	header["From"] = from
	header["To"] = e.Recipient
	header["Subject"] = e.Subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = "text/html; charset=\"utf-8\""

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + e.Body

	var auth smtp.Auth
	if smtpUser != "" {
		auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	}

	err := smtp.SendMail(smtpHost+":"+smtpPort, auth, fromEmail, to, []byte(message))
	if err != nil {
		slog.Error("Failed to send email", "recipient", e.Recipient, "error", err)
	} else {
		slog.Info("Email sent successfully", "recipient", e.Recipient)
	}
}
