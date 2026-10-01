package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/internal/model"
)

func saveRecordInput(ctx context.Context, tx *sql.Tx, id int64, input *pb.RegistrationRecordInput, now time.Time) error {
	// Old clients must never manufacture consent.
	if input == nil {
		return nil
	}
	var locked int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM contestants WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
		return err
	}
	record, err := model.FindRegistrationRecord(ctx, tx, id)
	if err != nil {
		return err
	}
	if input.AgreeTerms != nil {
		if !*input.AgreeTerms {
			return &model.InvalidError{Messages: []string{"参加規約への同意はこの操作では撤回できません"}}
		}
		if record.TermsAgreedAt == nil {
			record.TermsAgreedAt = &now
		}
	}
	if input.AgreePrivacy != nil {
		if !*input.AgreePrivacy {
			return &model.InvalidError{Messages: []string{"個人情報の取り扱いへの同意はこの操作では撤回できません"}}
		}
		if record.PrivacyAgreedAt == nil {
			record.PrivacyAgreedAt = &now
		}
	}
	if input.AgreeSponsor != nil {
		record.SetSponsor(*input.AgreeSponsor, now)
	}
	if input.MinorParentalConsent != nil {
		record.MinorParentalConsent = input.MinorParentalConsent
	}
	if input.Email != nil {
		record.Email = *input.Email
	}
	if input.TshirtSize != nil {
		switch *input.TshirtSize {
		case "", "S", "M", "L", "XL", "2XL", "3XL":
			record.TShirtSize = *input.TshirtSize
		default:
			return &model.InvalidError{Messages: []string{"Tシャツサイズが不正です"}}
		}
	}
	return model.SaveRegistrationRecord(ctx, tx, id, *record)
}

func recordPB(record *model.RegistrationRecord) *pb.RegistrationRecord {
	timestamp := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		value := t.Format(time.RFC3339Nano)
		return &value
	}
	return &pb.RegistrationRecord{TermsAgreedAt: timestamp(record.TermsAgreedAt), PrivacyAgreedAt: timestamp(record.PrivacyAgreedAt), SponsorProvideAgreedAt: timestamp(record.SponsorAgreedAt), SponsorProvideWithdrawnAt: timestamp(record.SponsorWithdrawnAt), MinorParentalConsent: record.MinorParentalConsent, Email: record.Email, TshirtSize: record.TShirtSize}
}

func (h *Handler) RegistrationRecordShow(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestantIncludeDisqualified(r)
	if err != nil {
		return err
	}
	if c == nil {
		return errNotFound("contestant")
	}
	return h.renderRegistrationRecord(w, r, c.ID)
}

// Consent changes remain available after registration and contest deadlines.
func (h *Handler) RegistrationRecordUpdate(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestantIncludeDisqualified(r)
	if err != nil {
		return err
	}
	if c == nil {
		return errNotFound("contestant")
	}
	input := &pb.RegistrationRecordInput{}
	if err := decodePB(r, input); err != nil {
		return err
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveRecordInput(r.Context(), tx, c.ID, input, info(r).now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return h.renderRegistrationRecord(w, r, c.ID)
}

func (h *Handler) AdminRegistrationRecord(w http.ResponseWriter, r *http.Request) error {
	id, ok := pathID(r)
	if !ok {
		return errNotFound("contestant")
	}
	var exists bool
	if err := h.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM registration_records WHERE contestant_id=$1 UNION ALL SELECT 1 FROM contestants WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errNotFound("record")
	}
	return h.renderRegistrationRecord(w, r, id)
}

func (h *Handler) renderRegistrationRecord(w http.ResponseWriter, r *http.Request, id int64) error {
	record, err := model.FindRegistrationRecord(r.Context(), h.db, id)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	renderPB(w, http.StatusOK, recordPB(record))
	return nil
}

// Source attributes (including unassigned users) are only available through the staff guard.
func (h *Handler) AdminRegistrationSource(w http.ResponseWriter, r *http.Request) error {
	id, ok := pathID(r)
	if !ok {
		return errNotFound("source")
	}
	var payload []byte
	err := h.db.QueryRowContext(r.Context(), `SELECT payload FROM registration_source_records WHERE model=$1 AND source_id=$2`, r.URL.Query().Get("model"), id).Scan(&payload)
	if err == sql.ErrNoRows {
		return errNotFound("source")
	}
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(json.RawMessage(payload))
}
