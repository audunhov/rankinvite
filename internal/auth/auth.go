package auth

import (
	"context"
	"database/sql"
	"fmt"
	"rankinvite/internal/db"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AdminUser struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
}

type Session struct {
	ID        string
	Email     string
	CSRFToken string
	ExpiresAt time.Time
}

type AuthService struct {
	queries *db.Queries
}

func NewAuthService(sqlDB *sql.DB) *AuthService {
	return &AuthService{
		queries: db.New(sqlDB),
	}
}

func (s *AuthService) CreateAdmin(email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return s.queries.CreateAdmin(context.Background(), db.CreateAdminParams{
		ID:           uuid.New().String(),
		Email:        email,
		PasswordHash: string(hash),
	})
}

func (s *AuthService) EnsureAdmin(email, password string) error {
	exists, err := s.queries.AdminExists(context.Background(), email)
	if err != nil {
		return err
	}

	if exists == 0 {
		return s.CreateAdmin(email, password)
	}
	return nil
}

func (s *AuthService) ListAdmins() ([]AdminUser, error) {
	admins, err := s.queries.ListAdmins(context.Background())
	if err != nil {
		return nil, err
	}
	
	results := make([]AdminUser, len(admins))
	for i, a := range admins {
		id, _ := uuid.Parse(a.ID)
		results[i] = AdminUser{
			ID:           id,
			Email:        a.Email,
			PasswordHash: a.PasswordHash,
		}
	}
	return results, nil
}

func (s *AuthService) DeleteAdmin(id string) error {
	return s.queries.DeleteAdmin(context.Background(), id)
}

func (s *AuthService) VerifyAdmin(email, password string) (*AdminUser, error) {
	admin, err := s.queries.GetAdmin(context.Background(), email)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password))
	if err != nil {
		return nil, nil // Invalid password
	}

	id, _ := uuid.Parse(admin.ID)
	return &AdminUser{
		ID:           id,
		Email:        admin.Email,
		PasswordHash: admin.PasswordHash,
	}, nil
}

func (s *AuthService) CreateSession(id, email, csrfToken string, expiresAt time.Time) error {
	return s.queries.CreateSession(context.Background(), db.CreateSessionParams{
		ID:        id,
		Email:     email,
		CsrfToken: csrfToken,
		ExpiresAt: expiresAt,
	})
}

func (s *AuthService) GetSession(id string) (*Session, error) {
	session, err := s.queries.GetSession(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if session.ExpiresAt.Before(time.Now()) {
		s.queries.DeleteSession(context.Background(), id)
		return nil, fmt.Errorf("session expired")
	}
	return &Session{
		ID:        session.ID,
		Email:     session.Email,
		CSRFToken: session.CsrfToken,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func (s *AuthService) DeleteSession(id string) error {
	return s.queries.DeleteSession(context.Background(), id)
}
