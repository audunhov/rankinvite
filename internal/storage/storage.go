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

func (r *InvitationRepository) List(limit, offset int32) ([]*models.Invitation, error) {
	rows, err := r.queries.ListInvitations(context.Background(), db.ListInvitationsParams{
		Limit:  int64(limit),
		Offset: int64(offset),
	})
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

func (r *InvitationRepository) ListFiltered(query, status string, limit, offset int32) ([]*models.Invitation, error) {
	rows, err := r.queries.ListInvitationsFiltered(context.Background(), db.ListInvitationsFilteredParams{
		Query:  query,
		Status: status,
		Limit:  int64(limit),
		Offset: int64(offset),
	})
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

func (r *InvitationRepository) Count() (int64, error) {
	return r.queries.CountInvitations(context.Background())
}

func (r *InvitationRepository) CountFiltered(query, status string) (int64, error) {
	return r.queries.CountInvitationsFiltered(context.Background(), db.CountInvitationsFilteredParams{
		Query:  query,
		Status: status,
	})
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

func (r *InvitationRepository) GetSetting(key string) (string, error) {
	return r.queries.GetSetting(context.Background(), key)
}

func (r *InvitationRepository) UpdateSetting(key, value string) error {
	return r.queries.UpdateSetting(context.Background(), db.UpdateSettingParams{
		Key:   key,
		Value: value,
	})
}
