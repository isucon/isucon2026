package domain

import "github.com/google/uuid"

type User struct {
	ID                 uuid.UUID
	Username           string
	UsernameNormalized string
	DisplayName        string
	Bio                string
	ProfileImageID     *uuid.UUID
	Region             string
	AccountType        string
	Status             string
}

type Administrator struct {
	ID        uuid.UUID
	LoginName string
	Enabled   bool
}
