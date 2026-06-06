package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"rankinvite/internal/auth"
	"rankinvite/internal/models"
	"rankinvite/internal/storage"
	"time"

	"github.com/google/uuid"
)

//go:embed templates/*.html
var templateFS embed.FS

type EventProcessor interface {
	ProcessEvents([]models.Event)
}

type Server struct {
	repo      *storage.InvitationRepository
	auth      *auth.AuthService
	worker    EventProcessor
	baseURL   string
	templates *template.Template
}

func NewServer(repo *storage.InvitationRepository, auth *auth.AuthService) *Server {
	tmpl := template.New("base").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	})
	
	// Strip "templates/" prefix from files in embedded FS
	subFS, err := fs.Sub(templateFS, "templates")
	if err != nil {
		slog.Error("Failed to access embedded templates directory", "error", err)
		return &Server{repo: repo, auth: auth, templates: tmpl}
	}
	
	parsed, err := tmpl.ParseFS(subFS, "*.html")
	if err != nil {
		slog.Error("Failed to parse embedded templates", "error", err)
	} else {
		tmpl = parsed
	}

	return &Server{
		repo:      repo,
		auth:      auth,
		templates: tmpl,
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
	mux.HandleFunc("/i/calendar/", s.handleInviteCalendar)
	
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	
	// Protected Admin Routes
	admin := func(h http.HandlerFunc) http.HandlerFunc {
		return s.requireAdmin(s.csrfMiddleware(h))
	}

	mux.HandleFunc("/admin", admin(s.handleAdminDashboard))
	mux.HandleFunc("/admin/invitations/new", admin(s.handleNewInvitation))
	mux.HandleFunc("/admin/invitations", admin(s.handleCreateInvitation))
	mux.HandleFunc("/admin/invitations/", admin(s.handleInvitationDetails))
	mux.HandleFunc("/admin/invitations/action", admin(s.handleAdminInvitationAction))
	mux.HandleFunc("/admin/invitations/delete", admin(s.handleDeleteInvitation))
	mux.HandleFunc("/admin/invitations/strategies", admin(s.handleCreateStrategy))
	mux.HandleFunc("/admin/invitations/status", admin(s.handleInvitationStatusPartial))
	mux.HandleFunc("/admin/invitations/update_template", admin(s.handleUpdateEmailTemplate))
	mux.HandleFunc("/admin/invitations/preview", admin(s.handlePreviewEmail))

	mux.HandleFunc("/admin/users", admin(s.handleListUsers))
	mux.HandleFunc("/admin/users/create", admin(s.handleCreateUser))
	mux.HandleFunc("/admin/users/delete", admin(s.handleDeleteUser))

	mux.HandleFunc("/admin/settings", admin(s.handleSettings))
	mux.HandleFunc("/admin/settings/update", admin(s.handleUpdateSettings))
}

type PageData struct {
	Invitation           *models.Invitation
	Invite               *models.PersonalInvite
	Invitations          []*models.Invitation
	Admins               []auth.AdminUser
	CurrentEmail         string
	PastEmails           []string
	DefaultEmailTemplate string
	Error                string
	CSRFToken            string
	Page                 int
	TotalPages           int
	HasNext              bool
	HasPrev              bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data PageData) {
	if session, ok := r.Context().Value(sessionKey).(*auth.Session); ok {
		data.CSRFToken = session.CSRFToken
		data.CurrentEmail = session.Email
	}
	err := s.templates.ExecuteTemplate(w, name, data)
	if err != nil {
		slog.Error("Template error", "name", name, "error", err)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, r, "login.html", PageData{})
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	user, err := s.auth.VerifyAdmin(email, password)
	if err != nil || user == nil {
		slog.Warn("Failed login attempt", "email", email, "error", err)
		s.render(w, r, "login.html", PageData{Error: "Feil e-post eller passord"})
		return
	}

	slog.Info("Successful login", "email", email)

	// Create persistent session
	b := make([]byte, 32)
	rand.Read(b)
	sessionID := base64.URLEncoding.EncodeToString(b)
	
	// Generate CSRF token
	c := make([]byte, 32)
	rand.Read(c)
	csrfToken := base64.URLEncoding.EncodeToString(c)
	
	expiresAt := time.Now().Add(24 * time.Hour)
	if err := s.auth.CreateSession(sessionID, email, csrfToken, expiresAt); err != nil {
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
	page := 1
	if pStr := r.URL.Query().Get("page"); pStr != "" {
		fmt.Sscanf(pStr, "%d", &page)
	}
	if page < 1 {
		page = 1
	}

	pageSize := int32(10)
	offset := int32(page-1) * pageSize

	invs, err := s.repo.List(pageSize, offset)
	if err != nil {
		slog.Error("Failed to list invitations", "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	total, _ := s.repo.Count()
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	s.render(w, r, "dashboard.html", PageData{
		Invitations: invs,
		Page:        page,
		TotalPages:  totalPages,
		HasNext:     page < totalPages,
		HasPrev:     page > 1,
	})
}

func (s *Server) handleNewInvitation(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "new_invitation.html", PageData{})
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

	defaultTemplate, _ := s.repo.GetSetting("default_email_template")

	inv := models.NewInvitation(title, spots)
	inv.Location = location
	inv.Description = description
	inv.StartTime = startTime
	inv.CustomEmailTemplate = defaultTemplate

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

	allInvs, err := s.repo.List(1000, 0) // Look in recent 1000 invitations
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

	s.render(w, r, "invite.html", PageData{
		Invitation: foundInv,
		Invite:     foundPersonalInvite,
	})
}

func (s *Server) handleInviteCalendar(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/i/calendar/"):]
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	allInvs, err := s.repo.List(1000, 0)
	var foundInv *models.Invitation
	for _, inv := range allInvs {
		for _, pi := range inv.PersonalInvites {
			if pi.ID == inviteID {
				foundInv = inv
				break
			}
		}
	}

	if foundInv == nil || foundInv.StartTime.IsZero() {
		http.NotFound(w, r)
		return
	}

	// Generate ICS
	start := foundInv.StartTime.UTC().Format("20060102T150405Z")
	end := foundInv.StartTime.Add(time.Hour).UTC().Format("20060102T150405Z")
	
	ics := fmt.Sprintf("BEGIN:VCALENDAR\r\n"+
		"VERSION:2.0\r\n"+
		"PRODID:-//RankInvite//NONSGML v1.0//EN\r\n"+
		"BEGIN:VEVENT\r\n"+
		"UID:%s\r\n"+
		"DTSTAMP:%s\r\n"+
		"DTSTART:%s\r\n"+
		"DTEND:%s\r\n"+
		"SUMMARY:%s\r\n"+
		"LOCATION:%s\r\n"+
		"DESCRIPTION:%s\r\n"+
		"END:VEVENT\r\n"+
		"END:VCALENDAR\r\n",
		inviteID, start, start, end, foundInv.Title, foundInv.Location, foundInv.Description)

	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.ics\"", foundInv.Title))
	w.Write([]byte(ics))
}

func (s *Server) handleInviteAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	inviteID, _ := uuid.Parse(r.FormValue("invite_id"))
	action := r.FormValue("action")

	allInvs, _ := s.repo.List(1000, 0)
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

	s.render(w, r, "invitation_details.html", PageData{
		Invitation: inv,
	})
}

func (s *Server) handleNewStrategy(w http.ResponseWriter, r *http.Request, idStr string) {
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	pastEmails, _ := s.repo.GetUniqueEmails()

	s.render(w, r, "new_strategy.html", PageData{
		Invitation: inv,
		PastEmails:  pastEmails,
	})
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
	} else if action == "cancel" {
		events = inv.Handle(models.Command{
			Type:    models.CmdCancel,
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

	s.templates.ExecuteTemplate(w, "status_table", struct {
		Invitation *models.Invitation
	}{inv})
}

func (s *Server) handleDeleteInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := s.repo.Delete(inviteID); err != nil {
		slog.Error("Failed to delete invitation", "id", inviteID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info("Invitation deleted", "id", inviteID)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	admins, err := s.auth.ListAdmins()
	if err != nil {
		slog.Error("Failed to list admins", "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	s.render(w, r, "users.html", PageData{
		Admins: admins,
	})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	if err := s.auth.CreateAdmin(email, password); err != nil {
		slog.Error("Failed to create admin", "email", email, "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	slog.Info("Admin user created", "email", email)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	idStr := r.FormValue("id")
	session := r.Context().Value(sessionKey).(*auth.Session)

	// Check if trying to delete self
	admins, _ := s.auth.ListAdmins()
	for _, a := range admins {
		if a.ID.String() == idStr && a.Email == session.Email {
			http.Error(w, "Du kan ikke slette din egen bruker", http.StatusForbidden)
			return
		}
	}

	if err := s.auth.DeleteAdmin(idStr); err != nil {
		slog.Error("Failed to delete admin", "id", idStr, "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	slog.Info("Admin user deleted", "id", idStr)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

type contextKey string

const sessionKey contextKey = "session"

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, err := s.auth.GetSession(cookie.Value)
		if err != nil {
			slog.Warn("Invalid or expired session", "session_id", cookie.Value, "error", err)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		slog.Debug("Admin access verified", "email", session.Email)
		
		// Store session in context
		ctx := r.Context()
		ctx = context.WithValue(ctx, sessionKey, session)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleUpdateEmailTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	if inv == nil || inv.Status != models.StatusDraft {
		http.Error(w, "Invalid invitation state", http.StatusBadRequest)
		return
	}

	inv.CustomEmailTemplate = r.FormValue("email_template")
	s.repo.Save(inv)

	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handlePreviewEmail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	if inv == nil {
		http.NotFound(w, r)
		return
	}

	// Use a dummy UUID for preview
	dummyID := uuid.New()
	html := inv.RenderEmailBody(dummyID, s.baseURL)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	defaultTemplate, _ := s.repo.GetSetting("default_email_template")
	s.render(w, r, "settings.html", PageData{
		DefaultEmailTemplate: defaultTemplate,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	template := r.FormValue("default_email_template")
	s.repo.UpdateSetting("default_email_template", template)

	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

func (s *Server) csrfMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			session, ok := r.Context().Value(sessionKey).(*auth.Session)
			if !ok {
				next(w, r)
				return
			}

			token := r.FormValue("csrf_token")
			if token == "" || token != session.CSRFToken {
				slog.Warn("CSRF validation failed", "email", session.Email)
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}
