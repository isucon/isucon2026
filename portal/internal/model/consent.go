package model

import (
	"context"
	"database/sql"
	"time"
)

// Consent preserves unknown values; absence never implies agreement.
type Consent struct {
	TermsAgreedAt        *time.Time `json:"terms_agreed_at"`
	PrivacyAgreedAt      *time.Time `json:"privacy_agreed_at"`
	SponsorAgreedAt      *time.Time `json:"sponsor_provide_agreed_at"`
	SponsorWithdrawnAt   *time.Time `json:"sponsor_provide_withdrawn_at"`
	MinorParentalConsent *bool      `json:"minor_parental_consent"`
}

// SetSponsor follows Django's current-state semantics. Repeated requests are no-ops.
func (c *Consent) SetSponsor(agreed bool, now time.Time) {
	if agreed == (c.SponsorAgreedAt != nil) {
		return
	}
	if agreed {
		c.SponsorAgreedAt = &now
		c.SponsorWithdrawnAt = nil
	} else {
		c.SponsorAgreedAt = nil
		c.SponsorWithdrawnAt = &now
	}
}

type RegistrationRecord struct {
	Consent
	Email      string `json:"email"`
	TShirtSize string `json:"tshirt_size"`
}

func FindRegistrationRecord(ctx context.Context, db DBTX, id int64) (*RegistrationRecord, error) {
	r := &RegistrationRecord{}
	err := db.QueryRowContext(ctx, `SELECT terms_agreed_at,privacy_agreed_at,sponsor_provide_agreed_at,sponsor_provide_withdrawn_at,minor_parental_consent,email,tshirt_size FROM registration_records WHERE contestant_id=$1`, id).Scan(&r.TermsAgreedAt, &r.PrivacyAgreedAt, &r.SponsorAgreedAt, &r.SponsorWithdrawnAt, &r.MinorParentalConsent, &r.Email, &r.TShirtSize)
	if err == sql.ErrNoRows {
		return r, nil
	}
	return r, err
}

func SaveRegistrationRecord(ctx context.Context, db DBTX, id int64, r RegistrationRecord) error {
	_, err := db.ExecContext(ctx, `INSERT INTO registration_records(contestant_id,github_id,terms_agreed_at,privacy_agreed_at,sponsor_provide_agreed_at,sponsor_provide_withdrawn_at,minor_parental_consent,email,tshirt_size) VALUES($1,(SELECT github_id FROM contestants WHERE id=$1),$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(contestant_id) DO UPDATE SET terms_agreed_at=EXCLUDED.terms_agreed_at,privacy_agreed_at=EXCLUDED.privacy_agreed_at,sponsor_provide_agreed_at=EXCLUDED.sponsor_provide_agreed_at,sponsor_provide_withdrawn_at=EXCLUDED.sponsor_provide_withdrawn_at,minor_parental_consent=EXCLUDED.minor_parental_consent,email=EXCLUDED.email,tshirt_size=EXCLUDED.tshirt_size`, id, r.TermsAgreedAt, r.PrivacyAgreedAt, r.SponsorAgreedAt, r.SponsorWithdrawnAt, r.MinorParentalConsent, r.Email, r.TShirtSize)
	return err
}
