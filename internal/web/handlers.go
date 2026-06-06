package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"rankinvite/internal/auth"
	"rankinvite/internal/models"
	"rankinvite/internal/storage"
	"time"

	"github.com/google/uuid"
)

type EventProcessor interface {
	ProcessEvents([]models.Event)
}

type Server struct {
	repo     *storage.InvitationRepository
	auth     *auth.AuthService
	worker   EventProcessor
	baseURL  string
}

func NewServer(repo *storage.InvitationRepository, auth *auth.AuthService) *Server {
	return &Server{
		repo: repo,
		auth: auth,
	}
}

func (s *Server) SetWorker(w EventProcessor) {
	s.worker = w
}

func (s *Server) SetBaseURL(u string) {
	s.baseURL = u
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/i/", s.handlePersonalInvite)
	mux.HandleFunc("/i/action", s.handleInviteAction)
	
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	
	mux.HandleFunc("/admin", s.requireAdmin(s.handleAdminDashboard))
	mux.HandleFunc("/admin/invitations/new", s.requireAdmin(s.handleNewInvitation))
	mux.HandleFunc("/admin/invitations", s.requireAdmin(s.handleCreateInvitation))
	mux.HandleFunc("/admin/invitations/", s.requireAdmin(s.handleInvitationDetails)) // This matches /admin/invitations/{id}
	mux.HandleFunc("/admin/invitations/action", s.requireAdmin(s.handleAdminInvitationAction))
	
	// Add this for processing the strategy form
	mux.HandleFunc("/admin/invitations/strategies", s.requireAdmin(s.handleCreateStrategy))
	
	// Add this for live status updates via HTMX
	mux.HandleFunc("/admin/invitations/status", s.requireAdmin(s.handleInvitationStatusPartial))
}

const headCommon = `
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/pruger/tiny-brutalism-css/tiny-brutalism.css">
    <script src="https://unpkg.com/htmx.org@2.0.0"></script>
    <style>
        body, button, input, select, textarea { font-family: 'JetBrains Mono', monospace !important; }
        .container { max-width: 1000px; margin: 40px auto; padding: 0 20px; }
        .status-badge { padding: 8px 16px; border: 3px solid black; font-size: 0.8em; font-weight: bold; display: inline-block; box-shadow: 4px 4px 0px 0px rgba(0,0,0,1); text-transform: uppercase; }
        .status-draft { background: #ffff00; color: black; }
        .status-active { background: #00ff00; color: black; }
        .status-closed { background: #ff0000; color: white; }
        .table { width: 100%; border-collapse: collapse; margin: 24px 0; }
        .table th, .table td { padding: 20px; text-align: left; border-bottom: 3px solid black; }
        .table thead th { background: #eee; text-transform: uppercase; font-size: 0.9em; border-top: 3px solid black; }
        .card { padding: 32px; border: 3px solid black; box-shadow: 8px 8px 0px 0px rgba(0,0,0,1); background: white; margin-bottom: 48px; }
        .form-group { margin-bottom: 24px; }
        label { display: block; font-weight: bold; margin-bottom: 12px; text-transform: uppercase; font-size: 0.9em; }
        input, select { margin-bottom: 8px; }
        .button { padding: 12px 24px; }
        .participant-item { 
            display: flex; align-items: center; background: white; 
            padding: 12px 20px; margin-bottom: 16px; border: 3px solid black;
            box-shadow: 4px 4px 0px 0px rgba(0,0,0,1);
            cursor: move;
        }
        .participant-item.dragging { opacity: 0.5; transform: scale(1.02); }
        .remove-btn { margin-left: auto; color: #ff0000; font-weight: bold; border: 3px solid black; background: white; box-shadow: 2px 2px 0px 0px rgba(0,0,0,1); cursor: pointer; padding: 4px 12px; text-transform: uppercase; font-size: 0.7em; }
        .remove-btn:active { box-shadow: 0px 0px 0px 0px rgba(0,0,0,1); transform: translate(2px, 2px); }
        .btn-success { background: #00ff00 !important; color: black !important; }
        .btn-danger { background: #ff0000 !important; color: white !important; }
        .btn-primary { background: #00ffff !important; color: black !important; }
        .card { padding: 32px; border: 3px solid black; box-shadow: 8px 8px 0px 0px rgba(0,0,0,1); background: white; margin-bottom: 48px; }
    </style>
`

const loginTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Login - RankInvite</title>
    ` + headCommon + `
</head>
<body class="container">
    <main style="max-width: 500px; margin: 100px auto;">
        <div class="card">
            <h1>RankInvite Admin</h1>
            {{if .Error}}<p style="color: red; font-weight: bold;">{{.Error}}</p>{{end}}
            <form method="POST">
                <label>Brukernavn</label>
                <input type="text" name="username" required autofocus>
                <label>Passord</label>
                <input type="password" name="password" required>
                <button type="submit" class="button btn-primary" style="margin-top: 20px;">LOGG INN</button>
            </form>
        </div>
    </main>
</body>
</html>
`

const dashboardTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Admin Dashboard - RankInvite</title>
    ` + headCommon + `
</head>
<body class="container">
    <header style="display: flex; justify-content: space-between; align-items: center; border-bottom: 3px solid black; margin-bottom: 40px; padding: 20px 0;">
        <strong>RANKINVITE ADMIN</strong>
        <a href="/logout" class="button">LOGG UT</a>
    </header>

    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;">
        <h1>Dashboard</h1>
        <a href="/admin/invitations/new" class="button btn-primary">+ NY INVITASJON</a>
    </div>

    <div class="card">
        <h2>Dine invitasjoner</h2>
        {{if .Invitations}}
        <table class="table">
            <thead>
                <tr>
                    <th>Tittel</th>
                    <th>Plasser</th>
                    <th>Status</th>
                    <th>Opprettet</th>
                    <th>Handling</th>
                </tr>
            </thead>
            <tbody>
                {{range .Invitations}}
                <tr>
                    <td><strong>{{.Title}}</strong></td>
                    <td>{{.Spots}}</td>
                    <td><span class="status-badge status-{{.Status}}">{{.Status}}</span></td>
                    <td>{{.CreatedAt.Format "02.01.2006"}}</td>
                    <td><a href="/admin/invitations/{{.ID}}" class="button" style="padding: 4px 12px; font-size: 0.7em;">SE DETALJER</a></td>
                </tr>
                {{end}}
            </tbody>
        </table>
        {{else}}
        <p>Ingen invitasjoner opprettet ennå.</p>
        {{end}}
    </div>
</body>
</html>
`

const invitationDetailsTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>{{.Invitation.Title}} - Admin</title>
    ` + headCommon + `
</head>
<body class="container">
    <header style="border-bottom: 3px solid black; margin-bottom: 20px; padding: 10px 0;">
        <a href="/admin" style="text-decoration: none; font-weight: bold; border-bottom: 2px solid black;">← TILBAKE TIL DASHBOARD</a>
    </header>

    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 30px;">
        <h1>{{.Invitation.Title}}</h1>
        <div>
            {{if eq .Invitation.Status "draft"}}
            <form action="/admin/invitations/action" method="POST" style="display: inline;">
                <input type="hidden" name="id" value="{{.Invitation.ID}}">
                <button type="submit" name="action" value="start" class="button btn-success">START UTSENDELSE</button>
            </form>
            {{else if eq .Invitation.Status "active"}}
            <form action="/admin/invitations/action" method="POST" style="display: inline;">
                <input type="hidden" name="id" value="{{.Invitation.ID}}">
                <button type="submit" name="action" value="force_next" class="button btn-danger" onclick="return confirm('Sikker på at du vil avbryte nåværende ventetid og gå videre?')">HOPP OVER VENTENDE</button>
            </form>
            {{else}}
            <span class="status-badge status-{{.Invitation.Status}}">{{.Invitation.Status}}</span>
            {{end}}
        </div>
    </div>

    <div class="card">
        <div style="display: grid; grid-template-cols: 1fr 1fr; gap: 20px; margin-bottom: 20px;">
            <div>
                <strong>STED</strong>
                <p>{{if .Invitation.Location}}{{.Invitation.Location}}{{else}}Ikke oppgitt{{end}}</p>
            </div>
            <div>
                <strong>TIDSPUNKT</strong>
                <p>{{if .Invitation.StartTime.IsZero}}Ikke oppgitt{{else}}{{.Invitation.StartTime.Format "02.01.2006 kl. 15:04"}}{{end}}</p>
            </div>
        </div>
        <div>
            <strong>BESKRIVELSE</strong>
            <p>{{if .Invitation.Description}}{{.Invitation.Description}}{{else}}Ingen beskrivelse{{end}}</p>
        </div>
    </div>

    <div class="card">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;">
            <h2>Fordelingsforløp</h2>
            {{if eq .Invitation.Status "draft"}}
            <a href="/admin/invitations/{{.Invitation.ID}}/strategies/new" class="button btn-primary">+ LEGG TIL STRATEGI</a>
            {{end}}
        </div>
        
        {{if .Invitation.Strategies}}
            {{range $index, $strat := .Invitation.Strategies}}
            <div style="border: 3px solid black; padding: 15px; margin-bottom: 15px; box-shadow: 4px 4px 0px 0px rgba(0,0,0,1);">
                <div style="font-weight: bold; text-transform: uppercase; font-size: 0.8em; background: black; color: white; display: inline-block; padding: 2px 8px; margin-bottom: 10px;">{{$strat.Type}}</div>
                <div>Deltakere: {{len $strat.Participants}}</div>
                {{if eq $strat.Type "priority_list"}}
                    <div>Frist per person: {{$strat.InviteDuration}}</div>
                {{end}}
            </div>
            {{end}}
        {{else}}
            <p>Ingen strategier er lagt til ennå.</p>
        {{end}}
    </div>

    {{if ne .Invitation.Status "draft"}}
    <div class="card" hx-get="/admin/invitations/status?id={{.Invitation.ID}}" hx-trigger="every 5s">
        {{template "status_table" .}}
    </div>
    {{end}}
</body>
</html>

{{define "status_table"}}
<h2>Status på utsendelse</h2>
<table class="table">
    <thead>
        <tr>
            <th>E-post</th>
            <th>Status</th>
            <th>Utløper</th>
        </tr>
    </thead>
    <tbody>
        {{range .Invitation.PersonalInvites}}
        <tr>
            <td>{{.ParticipantEmail}}</td>
            <td><span class="status-badge status-{{.Status}}">{{.Status}}</span></td>
            <td>{{if .ExpiresAt.IsZero}}-{{else}}{{.ExpiresAt.Format "15:04:05"}}{{end}}</td>
        </tr>
        {{end}}
    </tbody>
</table>
{{end}}
`

const newStrategyTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Legg til strategi - Admin</title>
    ` + headCommon + `
</head>
<body class="container">
    <header style="border-bottom: 3px solid black; margin-bottom: 20px; padding: 10px 0;">
        <a href="/admin/invitations/{{.Invitation.ID}}" style="font-weight: bold; text-decoration: none; border-bottom: 2px solid black;">← TILBAKE TIL INVITASJON</a>
    </header>

    <h1>Legg til strategi</h1>
    <div class="card">
        <form id="strategyForm" action="/admin/invitations/strategies" method="POST">
            <input type="hidden" name="invitation_id" value="{{.Invitation.ID}}">
            
            <label>Type strategi</label>
            <select name="type" id="strategyType" onchange="toggleDuration()">
                <option value="priority_list">Prioritert liste</option>
                <option value="fcfs">Førstemann til mølla (FCFS)</option>
            </select>

            <div id="durationGroup" style="margin-top: 20px;">
                <label id="durationLabel">Svarfrist (minutter)</label>
                <input type="number" name="duration_mins" value="120">
            </div>

            <div style="margin-top: 20px;">
                <label>Deltakere (dra og slipp for å prioritere)</label>
                <div id="participantList" style="margin-bottom: 20px;">
                    <!-- Participants will be added here -->
                </div>
                
                <div style="display: flex; gap: 10px;">
                    <input type="email" id="emailInput" list="pastEmails" placeholder="E-postadresse">
                    <datalist id="pastEmails">
                        {{range .PastEmails}}<option value="{{.}}">{{end}}
                    </datalist>
                    <button type="button" class="button btn-primary" onclick="addParticipant()">LEGG TIL</button>
                </div>
            </div>

            <input type="hidden" name="participants_json" id="participantsJson">
            <button type="submit" class="button btn-primary" style="width: 100%; margin-top: 40px; font-size: 1.2em;">LAGRE STRATEGI</button>
        </form>
    </div>

    <script>
        const participantList = document.getElementById('participantList');
        const participants = [];

        function addParticipant() {
            const input = document.getElementById('emailInput');
            const email = input.value.trim();
            if (!email || participants.includes(email)) return;

            participants.push(email);
            renderList();
            input.value = '';
            input.focus();
        }

        function removeParticipant(email) {
            const index = participants.indexOf(email);
            if (index > -1) {
                participants.splice(index, 1);
                renderList();
            }
        }

        function renderList() {
            participantList.innerHTML = '';
            participants.forEach((email, index) => {
                const item = document.createElement('div');
                item.className = 'participant-item';
                item.draggable = true;
                item.innerHTML = '<span style="margin-right: 15px; font-size: 1.2em;">☰</span>' +
                                 '<span style="font-weight: bold;">' + email + '</span>' +
                                 '<button type="button" class="remove-btn" onclick="removeParticipant(\'' + email + '\')">Fjern</button>';
                
                item.addEventListener('dragstart', () => item.classList.add('dragging'));
                item.addEventListener('dragend', () => item.classList.remove('dragging'));
                participantList.appendChild(item);
            });
            updateJson();
        }

        participantList.addEventListener('dragover', e => {
            e.preventDefault();
            const draggingItem = document.querySelector('.dragging');
            const afterElement = getDragAfterElement(participantList, e.clientY);
            if (afterElement == null) {
                participantList.appendChild(draggingItem);
            } else {
                participantList.insertBefore(draggingItem, afterElement);
            }
        });

        participantList.addEventListener('drop', e => {
            const items = [...participantList.querySelectorAll('.participant-item')];
            participants.length = 0;
            items.forEach(item => {
                participants.push(item.querySelector('span:nth-child(2)').innerText);
            });
            updateJson();
        });

        function getDragAfterElement(container, y) {
            const draggableElements = [...container.querySelectorAll('.participant-item:not(.dragging)')];
            return draggableElements.reduce((closest, child) => {
                const box = child.getBoundingClientRect();
                const offset = y - box.top - box.height / 2;
                if (offset < 0 && offset > closest.offset) {
                    return { offset: offset, element: child };
                } else {
                    return closest;
                }
            }, { offset: Number.NEGATIVE_INFINITY }).element;
        }

        function updateJson() {
            document.getElementById('participantsJson').value = JSON.stringify(participants);
        }

        function toggleDuration() {
            const type = document.getElementById('strategyType').value;
            const label = document.getElementById('durationLabel');
            label.innerText = type === 'priority_list' ? 'Svarfrist per person (minutter)' : 'Total tidsfrist for runden (minutter)';
        }

        document.getElementById('emailInput').addEventListener('keypress', e => {
            if (e.key === 'Enter') {
                e.preventDefault();
                addParticipant();
            }
        });
    </script>
</body>
</html>
`

const inviteTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Invitasjon: {{.Invitation.Title}}</title>
    ` + headCommon + `
</head>
<body class="container">
    <main style="max-width: 600px; margin: 60px auto;">
        <div class="card" style="text-align: center;">
            <h1 style="font-size: 3em; margin-bottom: 10px;">{{.Invitation.Title}}</h1>
            
            <div style="display: flex; justify-content: center; gap: 40px; margin: 30px 0; border-top: 3px solid black; border-bottom: 3px solid black; padding: 20px 0;">
                {{if .Invitation.Location}}
                <div>
                    <strong style="text-transform: uppercase; font-size: 0.8em;">Sted</strong>
                    <div>{{.Invitation.Location}}</div>
                </div>
                {{end}}
                {{if not .Invitation.StartTime.IsZero}}
                <div>
                    <strong style="text-transform: uppercase; font-size: 0.8em;">Når</strong>
                    <div>{{.Invitation.StartTime.Format "02.01.2006 kl. 15:04"}}</div>
                </div>
                {{end}}
            </div>

            {{if .Invitation.Description}}
            <div style="text-align: left; margin-bottom: 40px;">
                <strong style="text-transform: uppercase; font-size: 0.8em;">Beskrivelse</strong>
                <p>{{.Invitation.Description}}</p>
            </div>
            {{end}}

            {{if eq .Invite.Status "accepted"}}
                <div style="background: #00ff00; padding: 20px; border: 3px solid black; box-shadow: 4px 4px 0px 0px black; font-weight: bold; margin: 20px 0;">DU ER PÅMELDT! TAKK!</div>
            {{else if eq .Invite.Status "declined"}}
                <div style="background: #ff0000; color: white; padding: 20px; border: 3px solid black; box-shadow: 4px 4px 0px black; font-weight: bold; margin: 20px 0;">DU HAR TAKKET NEI.</div>
            {{else if eq .Invite.Status "timed_out"}}
                <div style="background: #ff0000; color: white; padding: 20px; border: 3px solid black; box-shadow: 4px 4px 0px black; font-weight: bold; margin: 20px 0;">DENNE INVITASJONEN ER IKKE LENGER GYLDIG.</div>
            {{else}}
                <p style="font-size: 1.2em; margin: 30px 0;">Du er herved invitert. Det er <strong>{{.Invitation.Spots}}</strong> ledige plasser.</p>
                
                <form action="/i/action" method="POST" style="display: flex; flex-direction: column; gap: 20px;">
                    <input type="hidden" name="invite_id" value="{{.Invite.ID}}">
                    <button type="submit" name="action" value="accept" class="button btn-success" style="font-size: 1.5em; padding: 20px;">JEG VIL DELTA</button>
                    <button type="submit" name="action" value="decline" class="button btn-danger" style="font-size: 1.2em; padding: 15px;">JEG KAN IKKE DELTA</button>
                </form>
                
                <p style="margin-top: 30px; font-size: 0.9em; font-weight: bold; text-transform: uppercase;">Svarfrist på dette tilbudet: {{.Invite.ExpiresAt.Format "02.01.2006 kl. 15:04"}}</p>
            {{end}}
        </div>
    </main>
</body>
</html>
`

const newInvitationTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Ny invitasjon - RankInvite</title>
    ` + headCommon + `
</head>
<body class="container">
    <header style="border-bottom: 3px solid black; margin-bottom: 20px; padding: 10px 0;">
        <a href="/admin" style="font-weight: bold; text-decoration: none; border-bottom: 2px solid black;">← TILBAKE TIL DASHBOARD</a>
    </header>

    <h1>NY INVITASJON</h1>
    <div class="card">
        <form action="/admin/invitations" method="POST">
            <label>Tittel på arrangementet</label>
            <input type="text" name="title" placeholder="f.eks. Debatt i NRK" required>

            <label style="margin-top: 20px; display: block;">Sted</label>
            <input type="text" name="location" placeholder="f.eks. Marienlyst">

            <label style="margin-top: 20px; display: block;">Tidspunkt</label>
            <input type="datetime-local" name="start_time">

            <label style="margin-top: 20px; display: block;">Beskrivelse</label>
            <textarea name="description" rows="5" placeholder="Mer detaljer om arrangementet..."></textarea>
            
            <label style="margin-top: 20px; display: block;">Antall tilgjengelige plasser</label>
            <input type="number" name="spots" value="1" min="1" required>
            
            <button type="submit" class="button btn-primary" style="width: 100%; margin-top: 30px;">LAGRE UTKAST</button>
        </form>
    </div>
</body>
</html>
`

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tmpl, _ := template.New("login").Parse(loginTemplate)
		tmpl.Execute(w, nil)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := s.auth.VerifyAdmin(username, password)
	if err != nil || user == nil {
		slog.Warn("Failed login attempt", "username", username, "error", err)
		tmpl, _ := template.New("login").Parse(loginTemplate)
		tmpl.Execute(w, struct{ Error string }{"Feil brukernavn eller passord"})
		return
	}

	slog.Info("Successful login", "username", username)

	// Create persistent session
	b := make([]byte, 32)
	rand.Read(b)
	sessionID := base64.URLEncoding.EncodeToString(b)
	
	expiresAt := time.Now().Add(24 * time.Hour)
	if err := s.auth.CreateSession(sessionID, username, expiresAt); err != nil {
		slog.Error("Failed to create session in database", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   false, // Set to true in production
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err == nil {
		s.auth.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "session_id",
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	invs, err := s.repo.ListAll()
	if err != nil {
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	tmpl, _ := template.New("dashboard").Parse(dashboardTemplate)
	tmpl.Execute(w, struct {
		Invitations []*models.Invitation
	}{invs})
}

func (s *Server) handleNewInvitation(w http.ResponseWriter, r *http.Request) {
	tmpl, _ := template.New("new_invitation").Parse(newInvitationTemplate)
	tmpl.Execute(w, nil)
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/invitations/new", http.StatusSeeOther)
		return
	}

	title := r.FormValue("title")
	location := r.FormValue("location")
	description := r.FormValue("description")
	
	var startTime time.Time
	if stStr := r.FormValue("start_time"); stStr != "" {
		startTime, _ = time.Parse("2006-01-02T15:04", stStr)
	}

	var spots int
	fmt.Sscanf(r.FormValue("spots"), "%d", &spots)

	inv := models.NewInvitation(title, spots)
	inv.Location = location
	inv.Description = description
	inv.StartTime = startTime

	err := s.repo.Save(inv)
	if err != nil {
		slog.Error("Failed to save new invitation", "error", err)
		http.Error(w, "Serverfeil ved lagring", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s", inv.ID), http.StatusSeeOther)
}

func (s *Server) handlePersonalInvite(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/i/"):]
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Ugyldig lenke", http.StatusBadRequest)
		return
	}

	allInvs, err := s.repo.ListAll()
	if err != nil {
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	var foundInv *models.Invitation
	var foundPersonalInvite *models.PersonalInvite

	for _, inv := range allInvs {
		for _, pi := range inv.PersonalInvites {
			if pi.ID == inviteID {
				foundInv = inv
				foundPersonalInvite = &pi
				break
			}
		}
	}

	if foundInv == nil {
		http.NotFound(w, r)
		return
	}

	tmpl, _ := template.New("invite").Parse(inviteTemplate)
	tmpl.Execute(w, struct {
		Invitation *models.Invitation
		Invite     *models.PersonalInvite
	}{foundInv, foundPersonalInvite})
}

func (s *Server) handleInviteAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	inviteID, _ := uuid.Parse(r.FormValue("invite_id"))
	action := r.FormValue("action")

	allInvs, _ := s.repo.ListAll()
	var targetInv *models.Invitation
	for _, inv := range allInvs {
		for _, pi := range inv.PersonalInvites {
			if pi.ID == inviteID {
				targetInv = inv
				break
			}
		}
	}

	if targetInv == nil {
		http.NotFound(w, r)
		return
	}

	cmdType := models.CmdDecline
	if action == "accept" {
		cmdType = models.CmdAccept
	}

	// Process logic
	slog.Info("Processing invite action", "id", inviteID, "action", action)
	events := targetInv.Handle(models.Command{
		Type:     cmdType,
		InviteID: inviteID,
		Now:      time.Now(),
		BaseURL:  s.baseURL,
	})

	// Save changes
	if err := s.repo.Save(targetInv); err != nil {
		slog.Error("Failed to save invitation after action", "id", targetInv.ID, "error", err)
	}

	// Process side effects (emails)
	if s.worker != nil {
		slog.Debug("Dispatching events to worker", "id", targetInv.ID, "count", len(events))
		s.worker.ProcessEvents(events)
	}

	// Redirect back to see updated status
	http.Redirect(w, r, fmt.Sprintf("/i/%s", inviteID), http.StatusSeeOther)
}

func (s *Server) handleInvitationDetails(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/admin/invitations/"):]
	if idStr == "" || idStr == "new" {
		return // Handled by other routes
	}
	
	// Check for /strategies/new suffix
	if len(idStr) > len("/strategies/new") && idStr[len(idStr)-len("/strategies/new"):] == "/strategies/new" {
		s.handleNewStrategy(w, r, idStr[:len(idStr)-len("/strategies/new")])
		return
	}

	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil {
		http.NotFound(w, r)
		return
	}

	tmpl, _ := template.New("details").Parse(invitationDetailsTemplate)
	tmpl.Execute(w, struct {
		Invitation *models.Invitation
	}{inv})
}

func (s *Server) handleNewStrategy(w http.ResponseWriter, r *http.Request, idStr string) {
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	pastEmails, _ := s.repo.GetUniqueEmails()

	tmpl, _ := template.New("new_strategy").Parse(newStrategyTemplate)
	tmpl.Execute(w, struct {
		Invitation *models.Invitation
		PastEmails []string
	}{inv, pastEmails})
}

func (s *Server) handleCreateStrategy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("invitation_id")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	
	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil {
		http.NotFound(w, r)
		return
	}
	
	stratType := models.StrategyType(r.FormValue("type"))
	var participants []string
	json.Unmarshal([]byte(r.FormValue("participants_json")), &participants)
	
	var modelParticipants []models.Participant
	for _, p := range participants {
		modelParticipants = append(modelParticipants, models.Participant{Email: p})
	}
	
	var inviteDuration time.Duration
	var totalDuration time.Duration
	var mins int
	fmt.Sscanf(r.FormValue("duration_mins"), "%d", &mins)
	
	if stratType == models.StrategyPriorityList {
		inviteDuration = time.Duration(mins) * time.Minute
	} else {
		totalDuration = time.Duration(mins) * time.Minute
	}
	
	inv.Strategies = append(inv.Strategies, models.Strategy{
		Type:           stratType,
		Participants:   modelParticipants,
		InviteDuration: inviteDuration,
		TotalDuration:  totalDuration,
	})
	
	s.repo.Save(inv)
	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handleAdminInvitationAction(w http.ResponseWriter, r *http.Request) {
	idStr := r.FormValue("id")
	action := r.FormValue("action")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		slog.Error("Invalid ID in admin action", "id", idStr, "error", err)
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	
	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil {
		slog.Error("Invitation not found for admin action", "id", inviteID)
		http.NotFound(w, r)
		return
	}
	
	slog.Info("Performing admin action", "id", inviteID, "action", action)
	var events []models.Event
	if action == "start" {
		events = inv.Handle(models.Command{
			Type:    models.CmdStart,
			Now:     time.Now(),
			BaseURL: s.baseURL,
		})
	} else if action == "force_next" {
		events = inv.Handle(models.Command{
			Type:    models.CmdForceNext,
			Now:     time.Now(),
			BaseURL: s.baseURL,
		})
	}
	
	if err := s.repo.Save(inv); err != nil {
		slog.Error("Failed to save invitation after admin action", "id", inviteID, "error", err)
	}

	if s.worker != nil {
		slog.Debug("Dispatching admin events to worker", "id", inviteID, "count", len(events))
		s.worker.ProcessEvents(events)
	}
	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handleInvitationStatusPartial(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil {
		http.NotFound(w, r)
		return
	}

	tmpl, _ := template.New("status").Parse(invitationDetailsTemplate)
	tmpl.ExecuteTemplate(w, "status_table", struct {
		Invitation *models.Invitation
	}{inv})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		username, err := s.auth.GetSession(cookie.Value)
		if err != nil {
			slog.Warn("Invalid or expired session", "session_id", cookie.Value, "error", err)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		slog.Debug("Admin access verified", "username", username)
		next(w, r)
	}
}
