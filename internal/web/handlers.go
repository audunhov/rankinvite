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
	"strings"
	"time"

	"github.com/google/uuid"
)

//go:embed templates/*.html
var templateFS embed.FS

type EventProcessor interface {
	ProcessEvents([]models.Event, *models.Invitation)
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

// Middlewares

func (s *Server) RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic recovered in HTTP handler", "error", err, "path", r.URL.Path)
				http.Error(w, "En uventet feil oppstod på serveren", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) SetupMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check for setup on non-setup paths
		if r.URL.Path == "/setup" || r.URL.Path == "/setup/post" || strings.HasPrefix(r.URL.Path, "/i/") {
			next.ServeHTTP(w, r)
			return
		}

		admins, err := s.auth.ListAdmins()
		if err != nil {
			slog.Error("Failed to check admins for setup", "error", err)
		}
		if len(admins) == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		ctx := context.WithValue(r.Context(), sessionKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			session, ok := r.Context().Value(sessionKey).(*auth.Session)
			if ok {
				token := r.FormValue("csrf_token")
				if token == "" || token != session.CSRFToken {
					slog.Warn("CSRF validation failed", "email", session.Email)
					http.Error(w, "Ugyldig CSRF-token", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) http.Handler {
	// Root redirect
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})

	// Public Invitation routes
	mux.HandleFunc("/i/", s.handlePersonalInvite)
	mux.HandleFunc("/i/action", s.handleInviteAction)
	mux.HandleFunc("/i/calendar/", s.handleInviteCalendar)

	// Auth & Setup
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/setup", s.handleSetup)
	mux.HandleFunc("/setup/post", s.handlePostSetup)

	// Protected Admin routes helper
	admin := func(h http.HandlerFunc) http.Handler {
		return s.AuthMiddleware(s.CSRFMiddleware(http.HandlerFunc(h)))
	}

	mux.Handle("/admin", admin(s.handleAdminDashboard))
	mux.Handle("/admin/invitations/new", admin(s.handleNewInvitation))
	mux.Handle("/admin/invitations/edit", admin(s.handleEditInvitation))
	mux.Handle("/admin/invitations/update", admin(s.handleUpdateInvitation))
	mux.Handle("/admin/invitations", admin(s.handleCreateInvitation))
	mux.Handle("/admin/invitations/", admin(s.handleInvitationDetails))
	mux.Handle("/admin/invitations/action", admin(s.handleAdminInvitationAction))
	mux.Handle("/admin/invitations/delete", admin(s.handleDeleteInvitation))
	mux.Handle("/admin/invitations/strategies", admin(s.handleCreateStrategy))
	mux.Handle("/admin/invitations/strategies/delete", admin(s.handleDeleteStrategy))
	mux.Handle("/admin/invitations/status", admin(s.handleInvitationStatusPartial))
	mux.Handle("/admin/invitations/update_template", admin(s.handleUpdateEmailTemplate))
	mux.Handle("/admin/invitations/preview", admin(s.handlePreviewEmail))

	mux.Handle("/admin/users", admin(s.handleListUsers))
	mux.Handle("/admin/users/create", admin(s.handleCreateUser))
	mux.Handle("/admin/users/delete", admin(s.handleDeleteUser))
	mux.Handle("/admin/users/change-password", admin(s.handleChangePassword))

	mux.Handle("/admin/invitations/subscribe", admin(s.handleSubscribe))
	mux.Handle("/admin/invitations/unsubscribe", admin(s.handleUnsubscribe))

	mux.Handle("/admin/settings", admin(s.handleSettings))
	mux.Handle("/admin/settings/update", admin(s.handleUpdateSettings))
	mux.Handle("/admin/settings/preview", admin(s.handlePreviewDefaultTemplate))

	// Global stack
	return s.RecoverMiddleware(s.SetupMiddleware(mux))
}

type PageData struct {
	Invitation           *models.Invitation
	Invite               *models.PersonalInvite
	Invitations          []*models.Invitation
	Admins               []auth.AdminUser
	CurrentEmail         string
	PastEmails           []string
	DefaultEmailTemplate string
	GlobalSenderName     string
	GlobalSenderEmail    string
	SMTPHost             string
	SMTPPort             string
	SMTPUser             string
	SMTPPass             string
	SharedSenders        string
	SharedSendersList    []string
	AdminName            string
	IsSubscribed         bool
	IsAdminEmailAllowed  bool
	AllowedDomain        string
	Error                string
	FlashMessage         string
	FlashType            string // "success" or "error"
	SearchQuery          string
	StatusFilter         string
	CSRFToken            string
	Page                 int
	TotalPages           int
	HasNext              bool
	HasPrev              bool
}

func (s *Server) setFlash(w http.ResponseWriter, message, flashType string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_message",
		Value:    message,
		Path:     "/",
		HttpOnly: true,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_type",
		Value:    flashType,
		Path:     "/",
		HttpOnly: true,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	admins, _ := s.auth.ListAdmins()
	if len(admins) > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, "setup.html", PageData{})
}

func (s *Server) handlePostSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	admins, _ := s.auth.ListAdmins()
	if len(admins) > 0 {
		http.Error(w, "Setup already completed", http.StatusForbidden)
		return
	}

	name := r.FormValue("name")
	email := r.FormValue("email")
	password := r.FormValue("password")

	if name == "" || email == "" || len(password) < 6 {
		s.setFlash(w, "Vennligst fyll ut alle felt korrekt. Passord må være minst 6 tegn.", "error")
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	// 1. Create Admin
	if err := s.auth.CreateAdmin(email, name, password); err != nil {
		slog.Error("Failed to create initial admin", "error", err)
		http.Error(w, "Kunne ikke opprette administrator", http.StatusInternalServerError)
		return
	}

	// 2. Save SMTP Settings
	globalSenderName := r.FormValue("global_sender_name")
	globalSenderEmail := r.FormValue("global_sender_email")

	s.repo.UpdateSetting("global_sender_name", globalSenderName)
	s.repo.UpdateSetting("global_sender_email", globalSenderEmail)
	s.repo.UpdateSetting("smtp_host", r.FormValue("smtp_host"))
	s.repo.UpdateSetting("smtp_port", r.FormValue("smtp_port"))
	s.repo.UpdateSetting("smtp_user", r.FormValue("smtp_user"))
	s.repo.UpdateSetting("smtp_pass", r.FormValue("smtp_pass"))

	s.setFlash(w, "Oppsett fullført! Logg inn med din nye bruker.", "success")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data PageData) {
	if session, ok := r.Context().Value(sessionKey).(*auth.Session); ok {
		data.CSRFToken = session.CSRFToken
		data.CurrentEmail = session.Email
	}

	// Handle flash messages
	if cookie, err := r.Cookie("flash_message"); err == nil {
		data.FlashMessage = cookie.Value
		// Clear cookie
		http.SetCookie(w, &http.Cookie{Name: "flash_message", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	}
	if cookie, err := r.Cookie("flash_type"); err == nil {
		data.FlashType = cookie.Value
		// Clear cookie
		http.SetCookie(w, &http.Cookie{Name: "flash_type", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
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

	searchQuery := r.URL.Query().Get("q")
	statusFilter := r.URL.Query().Get("status")

	pageSize := int32(10)
	offset := int32(page-1) * pageSize

	invs, err := s.repo.ListFiltered(searchQuery, statusFilter, pageSize, offset)
	if err != nil {
		slog.Error("Failed to list invitations", "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	total, err := s.repo.CountFiltered(searchQuery, statusFilter)
	if err != nil {
		slog.Error("Failed to count invitations", "error", err)
	}
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	data := PageData{
		Invitations:  invs,
		Page:         page,
		TotalPages:   totalPages,
		HasNext:      page < totalPages,
		HasPrev:      page > 1,
		SearchQuery:  searchQuery,
		StatusFilter: statusFilter,
	}

	if r.Header.Get("HX-Request") == "true" {
		s.templates.ExecuteTemplate(w, "invitation_table", data)
		return
	}

	s.render(w, r, "dashboard.html", data)
}

func (s *Server) handleNewInvitation(w http.ResponseWriter, r *http.Request) {
	session := r.Context().Value(sessionKey).(*auth.Session)
	admins, _ := s.auth.ListAdmins()
	adminName := ""
	for _, a := range admins {
		if a.Email == session.Email {
			adminName = a.Name
			break
		}
	}

	globalName, _ := s.repo.GetSetting("global_sender_name")
	globalEmail, _ := s.repo.GetSetting("global_sender_email")
	sharedSenders, _ := s.repo.GetSetting("shared_senders")

	allowedDomain := ""
	if idx := strings.Index(globalEmail, "@"); idx != -1 {
		allowedDomain = globalEmail[idx+1:]
	}

	isAdminEmailAllowed := false
	if allowedDomain != "" && strings.HasSuffix(session.Email, "@"+allowedDomain) {
		isAdminEmailAllowed = true
	}
	
	var sharedList []string
	for _, line := range strings.Split(sharedSenders, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			sharedList = append(sharedList, trimmed)
		}
	}

	s.render(w, r, "new_invitation.html", PageData{
		AdminName:           adminName,
		GlobalSenderName:    globalName,
		GlobalSenderEmail:   globalEmail,
		SharedSenders:       sharedSenders,
		SharedSendersList:   sharedList,
		IsAdminEmailAllowed: isAdminEmailAllowed,
		AllowedDomain:       allowedDomain,
	})
}

func (s *Server) handleEditInvitation(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
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

	if inv.Status != models.StatusDraft {
		s.setFlash(w, "Kan bare redigere utkast", "error")
		http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
		return
	}

	s.render(w, r, "edit_invitation.html", PageData{Invitation: inv})
}

func (s *Server) handleUpdateInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Ugyldig ID", http.StatusBadRequest)
		return
	}
	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil || inv.Status != models.StatusDraft {
		http.Error(w, "Ugyldig invitasjon", http.StatusBadRequest)
		return
	}

	inv.Title = r.FormValue("title")
	inv.Location = r.FormValue("location")
	inv.Description = r.FormValue("description")
	
	if inv.Title == "" {
		s.setFlash(w, "Tittel kan ikke være tom", "error")
		http.Redirect(w, r, "/admin/invitations/edit?id="+idStr, http.StatusSeeOther)
		return
	}

	if stStr := r.FormValue("start_time"); stStr != "" {
		t, err := time.Parse("2006-01-02T15:04", stStr)
		if err == nil {
			inv.StartTime = t
		} else {
			slog.Warn("Invalid start time format", "value", stStr, "error", err)
		}
	} else {
		inv.StartTime = time.Time{}
	}
	if etStr := r.FormValue("end_time"); etStr != "" {
		t, err := time.Parse("2006-01-02T15:04", etStr)
		if err == nil {
			inv.EndTime = t
		} else {
			slog.Warn("Invalid end time format", "value", etStr, "error", err)
		}
	} else {
		inv.EndTime = time.Time{}
	}

	if _, err := fmt.Sscanf(r.FormValue("spots"), "%d", &inv.Spots); err != nil || inv.Spots < 1 {
		s.setFlash(w, "Antall plasser må være minst 1", "error")
		http.Redirect(w, r, "/admin/invitations/edit?id="+idStr, http.StatusSeeOther)
		return
	}

	if err := s.repo.Save(inv); err != nil {
		slog.Error("Failed to update invitation", "error", err)
		http.Error(w, "Serverfeil ved lagring", http.StatusInternalServerError)
		return
	}

	s.setFlash(w, "Invitasjonen ble oppdatert!", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s", inv.ID), http.StatusSeeOther)
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/invitations/new", http.StatusSeeOther)
		return
	}

	title := r.FormValue("title")
	location := r.FormValue("location")
	description := r.FormValue("description")
	
	if title == "" {
		s.setFlash(w, "Tittel kan ikke være tom", "error")
		http.Redirect(w, r, "/admin/invitations/new", http.StatusSeeOther)
		return
	}
	
	var startTime, endTime time.Time
	if stStr := r.FormValue("start_time"); stStr != "" {
		t, err := time.Parse("2006-01-02T15:04", stStr)
		if err == nil {
			startTime = t
		} else {
			slog.Warn("Invalid start time format", "value", stStr, "error", err)
		}
	}
	if etStr := r.FormValue("end_time"); etStr != "" {
		t, err := time.Parse("2006-01-02T15:04", etStr)
		if err == nil {
			endTime = t
		} else {
			slog.Warn("Invalid end time format", "value", etStr, "error", err)
		}
	}

	var spots int
	if _, err := fmt.Sscanf(r.FormValue("spots"), "%d", &spots); err != nil || spots < 1 {
		s.setFlash(w, "Antall plasser må være minst 1", "error")
		http.Redirect(w, r, "/admin/invitations/new", http.StatusSeeOther)
		return
	}

	defaultTemplate, err := s.repo.GetSetting("default_email_template")
	if err != nil {
		slog.Error("Failed to get default email template", "error", err)
	}

	inv := models.NewInvitation(title, spots)
	inv.Location = location
	inv.Description = description
	inv.StartTime = startTime
	inv.EndTime = endTime
	inv.CustomEmailTemplate = defaultTemplate

	session := r.Context().Value(sessionKey).(*auth.Session)
	inv.CreatedBy = session.Email
	inv.Subscribers = append(inv.Subscribers, session.Email)

	// Process sender
	senderValue := r.FormValue("sender")
	if senderValue == "me" {
		admins, _ := s.auth.ListAdmins()
		for _, a := range admins {
			if a.Email == session.Email {
				inv.SenderName = a.Name
				inv.SenderEmail = a.Email
				break
			}
		}
	} else if senderValue == "system" {
		inv.SenderName, _ = s.repo.GetSetting("global_sender_name")
		inv.SenderEmail, _ = s.repo.GetSetting("global_sender_email")
	} else if senderValue != "" {
		// Expects format "Name <email@domain.com>"
		if idx := strings.Index(senderValue, "<"); idx != -1 && strings.HasSuffix(senderValue, ">") {
			inv.SenderName = strings.TrimSpace(senderValue[:idx])
			inv.SenderEmail = senderValue[idx+1 : len(senderValue)-1]
		} else {
			inv.SenderEmail = senderValue
		}
	}

	if err := s.repo.Save(inv); err != nil {
		slog.Error("Failed to save new invitation", "error", err)
		http.Error(w, "Serverfeil ved lagring", http.StatusInternalServerError)
		return
	}

	s.setFlash(w, "Ny invitasjon opprettet!", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s", inv.ID), http.StatusSeeOther)
}

func (s *Server) handlePersonalInvite(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/i/"):]
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Ugyldig lenke", http.StatusBadRequest)
		return
	}

	allInvs, err := s.repo.List(1000, 0)
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

func (s *Server) handleInviteAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	inviteID, _ := uuid.Parse(r.FormValue("invite_id"))
	action := r.FormValue("action")

	var cmdType models.CommandType
	switch action {
	case "accept":
		cmdType = models.CmdAccept
	case "reject":
		cmdType = models.CmdReject
	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

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
		s.worker.ProcessEvents(events, targetInv)
	}

	// Redirect back to see updated status
	http.Redirect(w, r, fmt.Sprintf("/i/%s", inviteID), http.StatusSeeOther)
}

func (s *Server) handleInviteCalendar(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/i/calendar/"):]
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Ugyldig ID", http.StatusBadRequest)
		return
	}

	allInvs, _ := s.repo.List(1000, 0)
	var foundInv *models.Invitation
	for _, inv := range allInvs {
		for _, pi := range inv.PersonalInvites {
			if pi.ID == inviteID {
				foundInv = inv
				break
			}
		}
	}

	if foundInv == nil {
		http.NotFound(w, r)
		return
	}

	// Generate ICS
	start := foundInv.StartTime.UTC().Format("20060102T150405Z")
	endTime := foundInv.EndTime
	if endTime.IsZero() {
		endTime = foundInv.StartTime.Add(time.Hour)
	}
	end := endTime.UTC().Format("20060102T150405Z")
	
	ics := fmt.Sprintf("BEGIN:VCALENDAR\r\n"+
		"VERSION:2.0\r\n"+
		"PRODID:-//RankInvite//NONSGML v1.0//EN\r\n"+
		"BEGIN:VEVENT\r\n"+
		"UID:%s@rankinvite\r\n"+
		"DTSTAMP:%s\r\n"+
		"DTSTART:%s\r\n"+
		"DTEND:%s\r\n"+
		"SUMMARY:%s\r\n"+
		"DESCRIPTION:%s\r\n"+
		"LOCATION:%s\r\n"+
		"END:VEVENT\r\n"+
		"END:VCALENDAR\r\n",
		inviteID, start, start, end, foundInv.Title, foundInv.Description, foundInv.Location)

	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=invitation.ics")
	w.Write([]byte(ics))
}

func (s *Server) handleInvitationDetails(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/admin/invitations/"):]
	if idStr == "" || idStr == "new" {
		return // Handled by other routes
	}
	
	if strings.HasSuffix(idStr, "/strategies/new") {
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

	session := r.Context().Value(sessionKey).(*auth.Session)
	isSubscribed := false
	for _, sub := range inv.Subscribers {
		if sub == session.Email {
			isSubscribed = true
			break
		}
	}

	admins, _ := s.auth.ListAdmins()
	adminName := ""
	for _, a := range admins {
		if a.Email == session.Email {
			adminName = a.Name
			break
		}
	}

	s.render(w, r, "invitation_details.html", PageData{
		Invitation:   inv,
		IsSubscribed: isSubscribed,
		AdminName:    adminName,
	})
}

func (s *Server) handleNewStrategy(w http.ResponseWriter, r *http.Request, idStr string) {
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
	pastEmails, err := s.repo.GetUniqueEmails()
	if err != nil {
		slog.Error("Failed to get unique emails", "error", err)
	}

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

	idStr := r.FormValue("id")
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
	if stratType != models.StrategyPriorityList && stratType != models.StrategyFCFS {
		http.Error(w, "Ugyldig strategitype", http.StatusBadRequest)
		return
	}

	var participants []string
	if err := json.Unmarshal([]byte(r.FormValue("participants_json")), &participants); err != nil {
		slog.Error("Failed to unmarshal participants list", "error", err)
		http.Error(w, "Ugyldig deltakerliste", http.StatusBadRequest)
		return
	}
	if len(participants) == 0 {
		s.setFlash(w, "Du må legge til minst én deltaker", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s/strategies/new", idStr), http.StatusSeeOther)
		return
	}
	
	var modelParticipants []models.Participant
	for _, p := range participants {
		modelParticipants = append(modelParticipants, models.Participant{Email: p})
	}
	
	var inviteDuration time.Duration
	var totalDuration time.Duration
	var mins int
	if _, err := fmt.Sscanf(r.FormValue("duration_mins"), "%d", &mins); err != nil || mins < 1 {
		s.setFlash(w, "Tidsfrist må være minst 1 minutt", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s/strategies/new", idStr), http.StatusSeeOther)
		return
	}
	
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
	s.setFlash(w, "Strategien ble lagt til!", "success")
	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handleDeleteStrategy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.FormValue("id")
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	if inv == nil || inv.Status != models.StatusDraft {
		http.Error(w, "Ugyldig invitasjon", http.StatusBadRequest)
		return
	}

	var index int
	fmt.Sscanf(r.FormValue("index"), "%d", &index)

	if index < 0 || index >= len(inv.Strategies) {
		http.Error(w, "Ugyldig indeks", http.StatusBadRequest)
		return
	}

	// Remove strategy at index
	inv.Strategies = append(inv.Strategies[:index], inv.Strategies[index+1:]...)

	if err := s.repo.Save(inv); err != nil {
		slog.Error("Failed to delete strategy", "error", err)
		http.Error(w, "Serverfeil ved lagring", http.StatusInternalServerError)
		return
	}

	s.setFlash(w, "Strategien ble slettet!", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/invitations/%s", inv.ID), http.StatusSeeOther)
}

func (s *Server) handleAdminInvitationAction(w http.ResponseWriter, r *http.Request) {
	idStr := r.FormValue("id")
	action := r.FormValue("action")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Ugyldig ID", http.StatusBadRequest)
		return
	}

	inv, err := s.repo.GetByID(inviteID)
	if err != nil || inv == nil {
		slog.Error("Invitation not found for admin action", "id", inviteID)
		http.NotFound(w, r)
		return
	}

	var cmdType models.CommandType
	switch action {
	case "start":
		cmdType = models.CmdStart
	case "cancel":
		cmdType = models.CmdCancel
	case "force_next":
		cmdType = models.CmdForceNext
	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	events := inv.Handle(models.Command{
		Type:    cmdType,
		Now:     time.Now(),
		BaseURL: s.baseURL,
	})

	if err := s.repo.Save(inv); err != nil {
		slog.Error("Failed to save invitation after admin action", "id", inviteID, "error", err)
	}

	if s.worker != nil {
		slog.Debug("Dispatching admin events to worker", "id", inviteID, "count", len(events))
		s.worker.ProcessEvents(events, inv)
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

	s.templates.ExecuteTemplate(w, "status_table", PageData{
		Invitation: inv,
	})
}

func (s *Server) handleDeleteInvitation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	idStr := r.FormValue("id")
	inviteID, err := uuid.Parse(idStr)
	if err != nil {
		slog.Error("Invalid ID for deletion", "id", idStr, "error", err)
		http.Error(w, "Ugyldig ID", http.StatusBadRequest)
		return
	}

	if err := s.repo.Delete(inviteID); err != nil {
		slog.Error("Failed to delete invitation", "id", inviteID, "error", err)
		http.Error(w, "Serverfeil ved sletting", http.StatusInternalServerError)
		return
	}

	s.setFlash(w, "Invitasjonen ble slettet", "success")
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
	name := r.FormValue("name")
	password := r.FormValue("password")

	if email == "" || !strings.Contains(email, "@") {
		s.setFlash(w, "Ugyldig e-postadresse", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if len(password) < 6 {
		s.setFlash(w, "Passordet må være minst 6 tegn", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	if err := s.auth.CreateAdmin(email, name, password); err != nil {
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
	admins, err := s.auth.ListAdmins()
	if err != nil {
		slog.Error("Failed to list admins for deletion check", "error", err)
	}
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

	s.setFlash(w, "Brukeren ble slettet", "success")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	session := r.Context().Value(sessionKey).(*auth.Session)
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	if len(newPassword) < 6 {
		s.setFlash(w, "Nytt passord må være minst 6 tegn", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	if newPassword != confirmPassword {
		s.setFlash(w, "Nye passord samsvarer ikke", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	// Verify current password
	user, err := s.auth.VerifyAdmin(session.Email, currentPassword)
	if err != nil || user == nil {
		s.setFlash(w, "Feil nåværende passord", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	err = s.auth.UpdatePassword(user.ID, newPassword)
	if err != nil {
		slog.Error("Failed to update password", "error", err)
		s.setFlash(w, "Serverfeil ved bytte av passord", "error")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	s.setFlash(w, "Passordet ble endret!", "success")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

type contextKey string

const sessionKey contextKey = "session"

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
	globalSenderName, _ := s.repo.GetSetting("global_sender_name")
	globalSenderEmail, _ := s.repo.GetSetting("global_sender_email")
	smtpHost, _ := s.repo.GetSetting("smtp_host")
	smtpPort, _ := s.repo.GetSetting("smtp_port")
	smtpUser, _ := s.repo.GetSetting("smtp_user")
	smtpPass, _ := s.repo.GetSetting("smtp_pass")
	sharedSenders, _ := s.repo.GetSetting("shared_senders")

	s.render(w, r, "settings.html", PageData{
		DefaultEmailTemplate: defaultTemplate,
		GlobalSenderName:     globalSenderName,
		GlobalSenderEmail:    globalSenderEmail,
		SMTPHost:             smtpHost,
		SMTPPort:             smtpPort,
		SMTPUser:             smtpUser,
		SMTPPass:             smtpPass,
		SharedSenders:        sharedSenders,
	})
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	idStr := r.FormValue("id")
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	if inv == nil {
		http.NotFound(w, r)
		return
	}

	session := r.Context().Value(sessionKey).(*auth.Session)
	newSubscribers := []string{}
	for _, sub := range inv.Subscribers {
		if sub != session.Email {
			newSubscribers = append(newSubscribers, sub)
		}
	}
	inv.Subscribers = newSubscribers
	s.repo.Save(inv)

	s.setFlash(w, "Du følger ikke lenger dette arrangementet", "success")
	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	idStr := r.FormValue("id")
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	if inv == nil {
		http.NotFound(w, r)
		return
	}

	session := r.Context().Value(sessionKey).(*auth.Session)
	alreadySubscribed := false
	for _, sub := range inv.Subscribers {
		if sub == session.Email {
			alreadySubscribed = true
			break
		}
	}

	if !alreadySubscribed {
		inv.Subscribers = append(inv.Subscribers, session.Email)
		s.repo.Save(inv)
	}

	s.setFlash(w, "Du følger nå dette arrangementet!", "success")
	http.Redirect(w, r, "/admin/invitations/"+idStr, http.StatusSeeOther)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defaultTemplate := r.FormValue("default_email_template")
	globalSenderName := r.FormValue("global_sender_name")
	globalSenderEmail := r.FormValue("global_sender_email")
	sharedSenders := r.FormValue("shared_senders")

	// Extract domain
	allowedDomain := ""
	if idx := strings.Index(globalSenderEmail, "@"); idx != -1 {
		allowedDomain = globalSenderEmail[idx+1:]
	}

	// Validate shared senders
	if allowedDomain != "" {
		lines := strings.Split(sharedSenders, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Extract email from "Name <email@domain.com>" or just "email@domain.com"
			email := line
			if idx := strings.Index(line, "<"); idx != -1 && strings.HasSuffix(line, ">") {
				email = line[idx+1 : len(line)-1]
			}
			
			if !strings.HasSuffix(email, "@"+allowedDomain) {
				s.setFlash(w, fmt.Sprintf("Ugyldig delt avsender: '%s' må tilhøre domenet %s", email, allowedDomain), "error")
				http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
				return
			}
		}
	}

	s.repo.UpdateSetting("default_email_template", defaultTemplate)
	s.repo.UpdateSetting("global_sender_name", globalSenderName)
	s.repo.UpdateSetting("global_sender_email", globalSenderEmail)
	s.repo.UpdateSetting("smtp_host", r.FormValue("smtp_host"))
	s.repo.UpdateSetting("smtp_port", r.FormValue("smtp_port"))
	s.repo.UpdateSetting("smtp_user", r.FormValue("smtp_user"))
	s.repo.UpdateSetting("smtp_pass", r.FormValue("smtp_pass"))
	s.repo.UpdateSetting("shared_senders", sharedSenders)

	s.setFlash(w, "Innstillingene ble lagret!", "success")
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

func (s *Server) handlePreviewDefaultTemplate(w http.ResponseWriter, r *http.Request) {
	defaultTemplate, _ := s.repo.GetSetting("default_email_template")

	// Create a dummy invitation to use the RenderEmailBody logic
	dummyInv := &models.Invitation{
		Title:               "Eksempel: Debatt i NRK",
		Location:            "Marienlyst, Oslo",
		StartTime:           time.Now().Add(24 * time.Hour),
		EndTime:             time.Now().Add(26 * time.Hour),
		Description:         "Dette er en eksempelbeskrivelse for å vise hvordan e-posten vil se ut med dine variabler.",
		CustomEmailTemplate: defaultTemplate,
	}

	dummyID := uuid.New()
	html := dummyInv.RenderEmailBody(dummyID, s.baseURL)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}
