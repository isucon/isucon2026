package handler

import (
	"crypto/hmac"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/token"
)

// checkerAuth は認証 2 方式（FR-E-4）: CheckerToken、無ければ HMAC-SHA384 署名（旧 AMI 互換）。
// 戻り値は team_id。失敗時はレスポンス済みで ok=false。
func (h *Handler) checkerAuth(w http.ResponseWriter, r *http.Request) (int64, bool) {
	q := r.URL.Query()
	tok := q.Get("token")
	teamIDParam := q.Get("team_id")
	signature := q.Get("signature")
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		if tok == "" {
			tok = r.PostFormValue("token")
		}
		if teamIDParam == "" {
			teamIDParam = r.PostFormValue("team_id")
		}
		if signature == "" {
			signature = r.PostFormValue("signature")
		}
	}
	return h.checkerCredentialsAuth(w, tok, teamIDParam, signature)
}

func (h *Handler) checkerCredentialsAuth(w http.ResponseWriter, tok, teamIDParam, signature string) (int64, bool) {
	switch {
	case tok != "":
		payload := token.VerifyChecker(h.cfg.CheckerSecret, tok)
		if payload == nil {
			renderPlain(w, http.StatusUnauthorized, "invalid token")
			return 0, false
		}
		return payload.TeamID, true
	case teamIDParam != "":
		mac := hmac.New(sha512.New384, []byte(h.cfg.SSHKeyAPISecret))
		mac.Write([]byte(teamIDParam))
		expected := hex.EncodeToString(mac.Sum(nil))
		if !secureCompare(expected, signature) {
			renderPlain(w, http.StatusBadRequest, "invalid signature")
			return 0, false
		}
		teamID, err := parseInt64(teamIDParam)
		if err != nil {
			renderPlain(w, http.StatusBadRequest, "invalid team_id")
			return 0, false
		}
		return teamID, true
	default:
		renderPlain(w, http.StatusBadRequest, "request not have token or signature")
		return 0, false
	}
}

// EnvCheckInfo: GET /api/env_check_info（FR-E-1: JSON）
func (h *Handler) EnvCheckInfo(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.checkerAuth(w, r)
	if !ok {
		return
	}
	team, err := model.FindTeam(r.Context(), h.db, teamID)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	if team == nil {
		renderPlain(w, http.StatusNotFound, "team not found")
		return
	}

	var amiIDs []string
	switch r.URL.Query().Get("name") {
	case "test-boot", "test-ssh":
		if h.cfg.TestAMIID != "" {
			amiIDs = []string{h.cfg.TestAMIID}
		} else {
			amiIDs = []string{}
		}
	case "contest":
		amiIDs = []string{h.cfg.ContestAMIID}
	default:
		renderPlain(w, http.StatusBadRequest, "unknown name param")
		return
	}
	first := ""
	if len(amiIDs) > 0 {
		first = amiIDs[0]
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ami_id":  first,
		"ami_ids": amiIDs,
		"az_id":   team.AvailabilityZone.String,
	})
}

// EnvCheckSSHPublicKeys: GET /api/ssh_public_keys（FR-E-2: text/plain、末尾改行）
func (h *Handler) EnvCheckSSHPublicKeys(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.checkerAuth(w, r)
	if !ok {
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT k.public_key, c.id, c.github_login
		FROM ssh_public_keys k JOIN contestants c ON c.id = k.contestant_id
		WHERE c.team_id = $1
		ORDER BY k.contestant_id ASC, k.id ASC`, teamID)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var key, login string
		var contestantID int64
		if err := rows.Scan(&key, &contestantID, &login); err != nil {
			renderInternalError(w, err)
			return
		}
		lines = append(lines, fmt.Sprintf("%s %d@%s", key, contestantID, login))
	}
	if err := rows.Err(); err != nil {
		renderInternalError(w, err)
		return
	}
	renderPlain(w, http.StatusOK, strings.Join(lines, "\n")+"\n")
}

type envCheckResultRequest struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	IPAddress    string `json:"ip_address"`
	Passed       bool   `json:"passed"`
	Message      string `json:"message"`
	AdminMessage string `json:"admin_message"`
	RawData      string `json:"raw_data"`
}

// EnvCheckCreate: POST /api/env_checks（FR-E-3: 空ボディで結果保存、FR-E-5: 副作用）
func (h *Handler) EnvCheckCreate(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ctx := r.Context()
	var input envCheckResultRequest
	var teamID int64
	var ok bool
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		renderPlain(w, http.StatusBadRequest, "invalid content type")
		return
	}
	if mediaType == "application/json" {
		// The AMI checker sends a JSON object with a boolean passed field.
		dec := json.NewDecoder(r.Body)
		var result *envCheckResultRequest
		if err := dec.Decode(&result); err != nil || result == nil {
			renderPlain(w, http.StatusBadRequest, "invalid JSON result")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			renderPlain(w, http.StatusBadRequest, "invalid JSON result")
			return
		}
		input = *result
		teamID, ok = h.checkerCredentialsAuth(w, input.Token, "", "")
	} else {
		// Keep the existing form and legacy signature clients compatible.
		teamID, ok = h.checkerAuth(w, r)
		input = envCheckResultRequest{
			Name: r.FormValue("name"), IPAddress: r.FormValue("ip_address"),
			Passed:  r.FormValue("passed") == "true" || r.FormValue("passed") == "1",
			Message: r.FormValue("message"), AdminMessage: r.FormValue("admin_message"),
			RawData: r.FormValue("raw_data"),
		}
	}
	if !ok {
		return
	}
	name, publicIP, passed := input.Name, input.IPAddress, input.Passed

	previous, err := model.LastEnvCheck(ctx, h.db, teamID, name)
	if err != nil {
		renderInternalError(w, err)
		return
	}

	check := &model.EnvCheck{
		TeamID:       teamID,
		Name:         name,
		IPAddress:    publicIP,
		Passed:       passed,
		Message:      input.Message,
		AdminMessage: input.AdminMessage,
		RawData:      input.RawData,
	}
	if err := model.CreateEnvCheck(ctx, h.db, check); err != nil {
		renderInternalError(w, err)
		return
	}

	if strings.HasPrefix(name, "contest") {
		if name != "contest-unknown" {
			num, _ := parseInt64(strings.TrimPrefix(name, "contest"))
			existing, err := model.FindInstanceByTeamAndNumber(ctx, h.db, teamID, num)
			if err != nil {
				renderInternalError(w, err)
				return
			}
			if existing != nil && existing.PublicIPv4Address.Valid && existing.PublicIPv4Address.String != publicIP {
				h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":face_with_monocle: *Updated server IP:* <https://%s/admin/teams/%d|team_id=%d> name=%s", h.cfg.PublicURLHost, teamID, teamID, name)})
			}
			// 競技終了後は更新しない（FR-E-5）
			if !contest.ContestEnded(ri.timing, ri.now) {
				// cloud_id はダミー、private は捏造値（EC2 同期とは独立の第 2 経路）
				if err := model.UpsertInstance(ctx, h.db, teamID, num,
					fmt.Sprintf("contest-%d-%d", teamID, num),
					pbresources.ContestantInstance_RUNNING,
					fmt.Sprintf("192.168.0.1%d", num),
					sql.NullString{String: publicIP, Valid: true}); err != nil {
					renderInternalError(w, err)
					return
				}
			}
		}

		// 失敗は 5 分デバウンスで Slack 通知
		if !passed && (previous == nil || previous.Passed || previous.CreatedAt.Add(5*time.Minute).Before(ri.now)) {
			h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":shocked_face_with_exploding_head: *Checker failed:* <https://%s/admin/teams/%d|team_id=%d> name=%s", h.cfg.PublicURLHost, teamID, teamID, name)})
		}
	}
	// 空ボディ（FR-E-3）
	w.WriteHeader(http.StatusNoContent)
}
