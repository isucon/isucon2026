package idempotency

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrRequestConflict = errors.New("idempotency key was already used with a different request")
	ErrInFlight        = errors.New("idempotent request is already in flight")
)

type Scope struct{ Type, Key string }

type Request struct {
	Scope       Scope
	Operation   string
	Key         uuid.UUID
	RequestHash string
}

type Result struct {
	Status int
	Header map[string][]string
	Body   []byte
}

type Callback func(context.Context) (Result, error)

type Store interface {
	Execute(context.Context, Request, Callback) (Result, bool, error)
}
