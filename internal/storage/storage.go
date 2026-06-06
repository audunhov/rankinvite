package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"rankinvite/internal/db"
	"rankinvite/internal/models"

	"github.com/google/uuid"
)

type InvitationRepository struct {
	queries *db.Queries
}

func NewInvitationRepository(sqlDB *sql.DB) *InvitationRepository {
	return &InvitationRepository{
		queries: db.New(sqlDB),
	}
}

func (r *InvitationRepository) Save(inv *models.Invitation) error {
	data, err := json.Marshal(inv)
	if err != nil {
		return err
	}

	return r.queries.SaveInvitation(context.Background(), db.SaveInvitationParams{
		ID:   inv.ID.String(),
		Data: string(data),
	})
}

func (r *InvitationRepository) GetByID(id uuid.UUID) (*models.Invitation, error) {
	row, err := r.queries.GetInvitation(context.Background(), id.String())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var inv models.Invitation
	err = json.Unmarshal([]byte(row.Data), &inv)
	return &inv, err
}

func (r *InvitationRepository) ListAll() ([]*models.Invitation, error) {
	rows, err := r.queries.ListInvitations(context.Background())
	if err != nil {
		return nil, err
	}

	var results []*models.Invitation
	for _, row := range rows {
		var inv models.Invitation
		if err := json.Unmarshal([]byte(row.Data), &inv); err != nil {
			return nil, err
		}
		results = append(results, &inv)
	}
	return results, nil
}

func (r *InvitationRepository) Delete(id uuid.UUID) error {
	return r.queries.DeleteInvitation(context.Background(), id.String())
}

func (r *InvitationRepository) GetUniqueEmails() ([]string, error) {
	rows, err := r.queries.GetAllParticipants(context.Background())
	if err != nil {
		return nil, err
	}

	emails := make([]string, 0, len(rows))
	for _, row := range rows {
		if email, ok := row.(string); ok {
			emails = append(emails, email)
		} else if bytes, ok := row.([]byte); ok {
			emails = append(emails, string(bytes))
		}
	}
	return emails, nil
}
