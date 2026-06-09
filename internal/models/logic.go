package models

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
)

const emailWrapper = `<!DOCTYPE html>
<html lang="no">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <!--[if !mso]><!-->
    <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <!--<![endif]-->
    <title>{{.Header}}</title>
</head>
<body style="margin: 0; padding: 0; font-family: 'JetBrains Mono', 'Courier New', Courier, monospace; background-color: #f0f0f0;">
    <table width="100%" border="0" cellspacing="0" cellpadding="0" bgcolor="#f0f0f0" style="background-color: #f0f0f0;">
        <tr>
            <td align="center" style="padding: 40px 10px;">
                <!-- Main Card with Shifted Shadow (box-shadow) -->
                <table width="100%" border="0" cellspacing="0" cellpadding="0" style="max-width: 600px; background-color: #ffffff; border: 3px solid #000000; box-shadow: 8px 8px 0px 0px #000000; margin: 0 auto; border-collapse: separate;">
                    <tr>
                        <td style="padding: 32px; font-family: 'JetBrains Mono', 'Courier New', Courier, monospace;">
                            <h1 style="font-size: 28px; line-height: 1.2; text-transform: uppercase; margin: 0; border-bottom: 3px solid #000000; padding-bottom: 20px; font-family: 'JetBrains Mono', 'Courier New', Courier, monospace; font-weight: bold; color: #000000;">{{.Header}}</h1>
                            <div style="font-size: 16px; line-height: 1.6; margin: 24px 0; font-family: 'JetBrains Mono', 'Courier New', Courier, monospace; color: #000000;">{{.Content}}</div>
                            {{if .URL}}
                            <div style="margin-top: 40px;">
                                <!-- Button with Shifted Shadow (box-shadow) -->
                                <a href="{{.URL}}" style="display: inline-block; padding: 12px 24px; color: #000000; text-decoration: none; font-weight: bold; text-transform: uppercase; font-size: 16px; font-family: 'JetBrains Mono', 'Courier New', Courier, monospace; background-color: #00ff00; border: 2px solid #000000; box-shadow: 4px 4px 0px 0px #000000;">{{.ButtonText}}</a>
                            </div>
                            {{end}}
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>`

func (i *Invitation) RenderEmailBody(inviteID uuid.UUID, baseURL string) string {
	content := ""
	url := fmt.Sprintf("%s/i/%s", baseURL, inviteID)

	var deadlineStr string
	for _, pi := range i.PersonalInvites {
		if pi.ID == inviteID && !pi.ExpiresAt.IsZero() {
			// We use a safe default if it's the preview (where ID won't match)
			deadlineStr = pi.ExpiresAt.Format("02.01.2006 15:04")
			break
		}
	}

	if i.CustomEmailTemplate == "" {
		content = fmt.Sprintf("Hei! Du er herved invitert til %s.", i.Title)
	} else {
		tmpl, err := template.New("email").Parse(i.CustomEmailTemplate)
		if err == nil {
			var buf bytes.Buffer
			
			startTimeStr := ""
			if !i.StartTime.IsZero() {
				startTimeStr = i.StartTime.Format("02.01.2006 15:04")
			}
			endTimeStr := ""
			if !i.EndTime.IsZero() {
				endTimeStr = i.EndTime.Format("15:04")
			}
			durationStr := ""
			if !i.StartTime.IsZero() && !i.EndTime.IsZero() {
				durationStr = i.EndTime.Sub(i.StartTime).String()
			}

			// If deadline is still empty (e.g. in preview), provide a dummy one
			if deadlineStr == "" {
				deadlineStr = time.Now().Add(24 * time.Hour).Format("02.01.2006 15:04")
			}

			data := struct {
				Title       string
				Location    string
				StartTime   string
				EndTime     string
				Duration    string
				Description string
				Deadline    string
				URL         string
			}{
				Title:       i.Title,
				Location:    i.Location,
				StartTime:   startTimeStr,
				EndTime:     endTimeStr,
				Duration:    durationStr,
				Description: i.Description,
				Deadline:    deadlineStr,
				URL:         url,
			}
			tmpl.Execute(&buf, data)
			content = buf.String()
		} else {
			content = fmt.Sprintf("Hei! Du er herved invitert til %s. (Feil i mal: %v)", i.Title, err)
		}
	}

	// Clean up newlines and convert to HTML breaks for maximum compatibility
	// But only if it doesn't look like it already has HTML tags
	if !strings.Contains(content, "<p>") && !strings.Contains(content, "<br>") && !strings.Contains(content, "</div>") {
		replacer := strings.NewReplacer("\r\n", "<br>", "\n", "<br>", "\r", "<br>")
		content = replacer.Replace(content)
	}

	wrapperTmpl, err := template.New("wrapper").Parse(emailWrapper)
	if err != nil {
		slog.Error("CRITICAL: Failed to parse hardcoded email wrapper", "error", err)
		return content // Return raw content if wrapper fails
	}
	var buf bytes.Buffer
	wrapperTmpl.Execute(&buf, struct {
		Header     string
		Content    string
		URL        string
		ButtonText string
	}{
		Header:     "Invitasjon til " + i.Title,
		Content:    content,
		URL:        url,
		ButtonText: "SVAR PÅ INVITASJON",
	})

	return buf.String()
}

func RenderGenericEmail(header, content, url, buttonText string) string {
	// Clean up newlines if no HTML tags are present
	if !strings.Contains(content, "<p>") && !strings.Contains(content, "<br>") && !strings.Contains(content, "</div>") {
		replacer := strings.NewReplacer("\r\n", "<br>", "\n", "<br>", "\r", "<br>")
		content = replacer.Replace(content)
	}

	wrapperTmpl, err := template.New("wrapper").Parse(emailWrapper)
	if err != nil {
		slog.Error("CRITICAL: Failed to parse hardcoded email wrapper", "error", err)
		return content
	}
	var buf bytes.Buffer
	wrapperTmpl.Execute(&buf, struct {
		Header     string
		Content    string
		URL        string
		ButtonText string
	}{
		Header:     header,
		Content:    content,
		URL:        url,
		ButtonText: buttonText,
	})

	return buf.String()
}

type Event interface{}

type EmailSentEvent struct {
	Recipient string
	Subject   string
	URL       string
	Body      string
}

type ReminderEmailSentEvent struct {
	Recipient string
	Subject   string
	URL       string
	Body      string
}

type InvitationClosedEvent struct {
	Reason string
}

type InvitationFullyBookedEvent struct {
	InvitationID uuid.UUID
	Title        string
	Subscribers  []string
}

type DistributionPlanCompletedEvent struct {
	InvitationID   uuid.UUID
	Title          string
	Subscribers    []string
	RemainingSpots int
}

type SpotFilledEvent struct {
	ParticipantEmail string
	RemainingSpots   int
}

type CommandType string

const (
	CmdAccept    CommandType = "accept"
	CmdReject    CommandType = "reject"
	CmdDecline   CommandType = "decline"
	CmdTick      CommandType = "tick"
	CmdStart     CommandType = "start"
	CmdCancel    CommandType = "cancel"
	CmdForceNext CommandType = "force_next"
	CmdResend    CommandType = "resend"
)

type Command struct {
	Type     CommandType
	InviteID uuid.UUID // Optional
	Now      time.Time
	BaseURL  string
}

func (i *Invitation) Handle(cmd Command) []Event {
	var events []Event

	switch cmd.Type {
	case CmdAccept:
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
						events = append(events, InvitationFullyBookedEvent{
							InvitationID: i.ID,
							Title:        i.Title,
							Subscribers:  i.Subscribers,
						})
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
					events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
				}
				break
			}
		}

	case CmdReject, CmdDecline:
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.ID == cmd.InviteID && invite.Status == StatusPending {
				invite.Status = StatusDeclined
				events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
				break
			}
		}

	case CmdForceNext:
		if i.Status != StatusActive {
			return nil
		}
		// Time out all currently pending invites
		for idx := range i.PersonalInvites {
			if i.PersonalInvites[idx].Status == StatusPending {
				i.PersonalInvites[idx].Status = StatusTimedOut
			}
		}
		// Move to next step
		events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)

	case CmdTick:
		if i.Status != StatusActive {
			return nil
		}
		// Check for timeouts
		changed := false
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.Status == StatusPending && cmd.Now.After(invite.ExpiresAt) {
				invite.Status = StatusTimedOut
				changed = true
			}
		}

		// Check for reminders
		for idx := range i.PersonalInvites {
			invite := &i.PersonalInvites[idx]
			if invite.Status == StatusPending && !invite.ReminderSent {
				// Send reminder if less than 50% time left
				// This is a simple logic, could be more complex
				remaining := invite.ExpiresAt.Sub(cmd.Now)
				// For now: reminder if 30 mins left
				if remaining < 30*time.Minute {
					invite.ReminderSent = true
					events = append(events, ReminderEmailSentEvent{
						Recipient: invite.ParticipantEmail,
						Subject:   "PÅMINNELSE: Invitasjon til " + i.Title,
						URL:       fmt.Sprintf("%s/i/%s", cmd.BaseURL, invite.ID),
						Body:      i.RenderEmailBody(invite.ID, cmd.BaseURL),
					})
				}
			}
		}

		if changed {
			events = append(events, i.activateCurrentStrategy(cmd.Now, cmd.BaseURL)...)
		}

	case CmdStart:
		if i.Status != StatusDraft {
			return nil
		}
		i.Status = StatusActive
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

	case CmdResend:
		for _, pi := range i.PersonalInvites {
			if pi.ID == cmd.InviteID {
				events = append(events, EmailSentEvent{
					Recipient: pi.ParticipantEmail,
					Subject:   "Invitasjon til " + i.Title,
					URL:       fmt.Sprintf("%s/i/%s", cmd.BaseURL, pi.ID),
					Body:      i.RenderEmailBody(pi.ID, cmd.BaseURL),
				})
				break
			}
		}
	}

	return events
}

func (i *Invitation) activateCurrentStrategy(now time.Time, baseURL string) []Event {
	var events []Event

	if i.Status != StatusActive || i.Spots == 0 {
		return events
	}

	if i.CurrentStrategyIndex >= len(i.Strategies) {
		// Check if any invites are still pending
		anyPending := false
		for _, pi := range i.PersonalInvites {
			if pi.Status == StatusPending {
				anyPending = true
				break
			}
		}

		if !anyPending {
			i.Status = StatusClosed
			events = append(events, DistributionPlanCompletedEvent{
				InvitationID:   i.ID,
				Title:          i.Title,
				Subscribers:    i.Subscribers,
				RemainingSpots: i.Spots,
			})
			events = append(events, InvitationClosedEvent{Reason: "All strategies exhausted"})
		}
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

		if activeCount < i.Spots {
			// Find next person to invite
			sentAny := false
			for _, participant := range strategy.Participants {
				if activeCount >= i.Spots {
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
					i.PersonalInvites = append(i.PersonalInvites, PersonalInvite{
						ID:               inviteID,
						ParticipantEmail: participant.Email,
						Status:           StatusPending,
						ExpiresAt:        now.Add(strategy.InviteDuration),
					})
					events = append(events, EmailSentEvent{
						Recipient: participant.Email,
						Subject:   "Invitasjon til " + i.Title,
						URL:       fmt.Sprintf("%s/i/%s", baseURL, inviteID),
						Body:      i.RenderEmailBody(inviteID, baseURL),
					})
					activeCount++
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

	case StrategyFCFS:
		// Invite everyone in this strategy who hasn't been invited yet
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
				i.PersonalInvites = append(i.PersonalInvites, PersonalInvite{
					ID:               inviteID,
					ParticipantEmail: participant.Email,
					Status:           StatusPending,
					ExpiresAt:        now.Add(strategy.TotalDuration),
				})
				events = append(events, EmailSentEvent{
					Recipient: participant.Email,
					Subject:   "Invitasjon til " + i.Title,
					URL:       fmt.Sprintf("%s/i/%s", baseURL, inviteID),
					Body:      i.RenderEmailBody(inviteID, baseURL),
				})
				sentAny = true
			}
		}
		
		// For FCFS, we just wait until it's finished or someone accepts
		if !sentAny {
			stillPending := false
			for _, pi := range i.PersonalInvites {
				if pi.Status == StatusPending {
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
