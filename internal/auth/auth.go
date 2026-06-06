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
	Username     string
	PasswordHash string
}

type AuthService struct {
	queries *db.Queries
}

func NewAuthService(sqlDB *sql.DB) *AuthService {
	return &AuthService{
		queries: db.New(sqlDB),
	}
}

func (s *AuthService) CreateAdmin(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return s.queries.CreateAdmin(context.Background(), db.CreateAdminParams{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: string(hash),
	})
}

func (s *AuthService) EnsureAdmin(username, password string) error {
	exists, err := s.queries.AdminExists(context.Background(), username)
	if err != nil {
		return err
	}

	if exists == 0 {
		return s.CreateAdmin(username, password)
	}
	return nil
}

func (s *AuthService) VerifyAdmin(username, password string) (*AdminUser, error) {
	admin, err := s.queries.GetAdmin(context.Background(), username)
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
		Username:     admin.Username,
		PasswordHash: admin.PasswordHash,
	}, nil
}

func (s *AuthService) CreateSession(id, username string, expiresAt time.Time) error {
	return s.queries.CreateSession(context.Background(), db.CreateSessionParams{
		ID:        id,
		Username:  username,
		ExpiresAt: expiresAt,
	})
}

func (s *AuthService) GetSession(id string) (string, error) {
	session, err := s.queries.GetSession(context.Background(), id)
	if err != nil {
		return "", err
	}
	if session.ExpiresAt.Before(time.Now()) {
		s.queries.DeleteSession(context.Background(), id)
		return "", fmt.Errorf("session expired")
	}
	return session.Username, nil
}

func (s *AuthService) DeleteSession(id string) error {
	return s.queries.DeleteSession(context.Background(), id)
}
