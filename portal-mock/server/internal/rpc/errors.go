package rpc

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

func connectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrInvalidArgument):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, store.ErrFailedPrecondition):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal portal error"))
	}
}

func teamError(teamNumber, configured uint32) error {
	if teamNumber == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("team_number must be positive"))
	}
	if teamNumber != configured {
		return connect.NewError(connect.CodeNotFound, errors.New("team was not found"))
	}
	return nil
}
