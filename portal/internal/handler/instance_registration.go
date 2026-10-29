package handler

import (
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"regexp"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/model"
)

var ec2InstanceID = regexp.MustCompile(`^i-([0-9a-f]{8}|[0-9a-f]{17})$`)

type instanceRegistration struct {
	Token      string `json:"token"`
	Number     int64  `json:"number"`
	InstanceID string `json:"instance_id"`
	PrivateIP  string `json:"private_ipv4_address"`
	PublicIP   string `json:"public_ipv4_address"`
}

// InstanceRegister uses the same team-scoped checker token as envcheck. It only
// registers addresses; successful registration does not mean validation passed.
func (h *Handler) InstanceRegister(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		renderPlain(w, 415, "content type must be application/json")
		return
	}
	var input instanceRegistration
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF {
		renderPlain(w, 400, "invalid JSON registration")
		return
	}
	teamID, ok := h.checkerAuth(w, input.Token)
	if !ok {
		return
	}
	private, privateErr := netip.ParseAddr(input.PrivateIP)
	public, publicErr := netip.ParseAddr(input.PublicIP)
	if input.Number < 1 || input.Number > 5 || !ec2InstanceID.MatchString(input.InstanceID) || privateErr != nil || publicErr != nil || !private.Is4() || !public.Is4() || !private.IsPrivate() || !public.IsGlobalUnicast() || public.IsPrivate() {
		renderPlain(w, 400, "invalid instance metadata")
		return
	}
	team, err := model.FindTeam(r.Context(), h.db, teamID)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	if team == nil {
		renderPlain(w, 404, "team not found")
		return
	}
	ri := info(r)
	if contest.ContestEnded(ri.timing, ri.now) {
		renderPlain(w, 409, "contest has ended")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	defer tx.Rollback()
	if input.Number == 5 {
		err = model.RegisterTeamRouter(r.Context(), tx, teamID, input.InstanceID, private.String(), public.String())
	} else {
		err = model.ExpectTeamRouter(r.Context(), tx, teamID)
		if err == nil {
			err = model.UpsertInstance(r.Context(), tx, teamID, input.Number, input.InstanceID, pbresources.ContestantInstance_RUNNING, private.String(), sql.NullString{String: public.String(), Valid: true})
		}
	}
	if err != nil {
		renderInternalError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		renderInternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
