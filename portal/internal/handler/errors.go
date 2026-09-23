package handler

import (
	"errors"
	"net/http"

	"github.com/isucon/isucon2026/portal/internal/model"
)

// apiError は Api::ApplicationController::Error 系（CT-1: エラーも protobuf）。
type apiError struct {
	status  int
	name    string
	message string
	details []string
}

func (e *apiError) Error() string { return e.message }

func errNotFound(msg string) error {
	if msg == "" {
		msg = "Not found"
	}
	return &apiError{status: http.StatusNotFound, name: "Error::NotFound", message: msg}
}

func errBadRequest(msg string) error {
	if msg == "" {
		msg = "Bad request"
	}
	return &apiError{status: http.StatusBadRequest, name: "Error::BadRequest", message: msg}
}

func errForbidden(msg string) error {
	if msg == "" {
		msg = "Forbidden"
	}
	return &apiError{status: http.StatusForbidden, name: "Error::Forbidden", message: msg}
}

// errRegistrationClosed は Contest::RegistrationClosed 相当（422）。
func errRegistrationClosed() error {
	return &apiError{
		status:  422,
		name:    "Contest::RegistrationClosed",
		message: "Registration is closed",
		details: []string{"Registration is closed", "参加登録を受け付けていません (定員、開始前、もしくは締切超過)"},
	}
}

// renderAPIError は各エラー型を protobuf エラーレスポンスに落とす。
func (h *Handler) renderAPIError(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		details := ae.details
		if details == nil {
			details = []string{}
		}
		renderErrorFull(w, ae.status, ae.name, ae.message, details)
		return
	}
	var nf *model.NotFoundError
	if errors.As(err, &nf) {
		renderErrorFull(w, http.StatusNotFound, "ActiveRecord::RecordNotFound", "The requested resource was not found", nil)
		return
	}
	var inv *model.InvalidError
	if errors.As(err, &inv) {
		renderErrorFull(w, 422, "ActiveRecord::RecordInvalid", "Failed to save record", inv.Messages)
		return
	}
	if errors.Is(err, model.ErrInvalidTransition) {
		renderErrorFull(w, http.StatusBadRequest, "BenchmarkJob::InvalidTransition", err.Error(), nil)
		return
	}
	renderInternalError(w, err)
}

// api は protobuf API ハンドラのエラー処理アダプタ。
func (h *Handler) api(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			h.renderAPIError(w, err)
		}
	}
}
