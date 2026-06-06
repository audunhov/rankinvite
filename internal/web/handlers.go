package web

import (
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
	tmpl := template.New("")
	
	// Strip "templates/" prefix from files in embedded FS
	subFS, _ := fs.Sub(templateFS, "templates")
	
	tmpl, err := tmpl.ParseFS(subFS, "*.html")
	if err != nil {
		slog.Error("Failed to parse embedded templates", "error", err)
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.templates.ExecuteTemplate(w, "login.html", nil)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := s.auth.VerifyAdmin(username, password)
	if err != nil || user == nil {
		slog.Warn("Failed login attempt", "username", username, "error", err)
		s.templates.ExecuteTemplate(w, "login.html", struct{ Error string }{"Feil brukernavn eller passord"})
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
		slog.Error("Failed to list invitations", "error", err)
		http.Error(w, "Serverfeil", http.StatusInternalServerError)
		return
	}

	s.templates.ExecuteTemplate(w, "dashboard.html", struct {
		Invitations []*models.Invitation
	}{invs})
}

func (s *Server) handleNewInvitation(w http.ResponseWriter, r *http.Request) {
	s.templates.ExecuteTemplate(w, "new_invitation.html", nil)
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

	s.templates.ExecuteTemplate(w, "invite.html", struct {
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

	s.templates.ExecuteTemplate(w, "invitation_details.html", struct {
		Invitation *models.Invitation
	}{inv})
}

func (s *Server) handleNewStrategy(w http.ResponseWriter, r *http.Request, idStr string) {
	inviteID, _ := uuid.Parse(idStr)
	inv, _ := s.repo.GetByID(inviteID)
	pastEmails, _ := s.repo.GetUniqueEmails()

	s.templates.ExecuteTemplate(w, "new_strategy.html", struct {
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

	s.templates.ExecuteTemplate(w, "status_table", struct {
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
