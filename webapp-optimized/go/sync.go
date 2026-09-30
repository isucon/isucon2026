package isureal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// stateEvent is a versioned change to the hot state. It carries the new state
// of one key, never a delta, so applying events in any order converges.
type stateEvent struct {
	Version int64      `json:"v"`
	Kind    string     `json:"k"`
	A       string     `json:"a,omitempty"`
	B       string     `json:"b,omitempty"`
	Exists  bool       `json:"e,omitempty"`
	User    *eventUser `json:"u,omitempty"`
	Post    *Post      `json:"p,omitempty"`
	Window  *hotWindow `json:"w,omitempty"`
}

// eventUser is User with the fields User hides from API responses.
type eventUser struct {
	ID             string  `json:"id"`
	Username       string  `json:"username"`
	SecretKeyHash  string  `json:"secretKeyHash"`
	DisplayName    string  `json:"displayName"`
	Bio            string  `json:"bio"`
	ProfileImageID *string `json:"profileImageId"`
	Region         string  `json:"region"`
	AccountType    string  `json:"accountType"`
	Status         string  `json:"status"`
}

func newEventUser(u User) *eventUser {
	return &eventUser{ID: u.ID, Username: u.Username, SecretKeyHash: u.SecretKeyHash, DisplayName: u.DisplayName,
		Bio: u.Bio, ProfileImageID: u.ProfileImageID, Region: u.Region, AccountType: u.AccountType, Status: u.Status}
}

func (u *eventUser) user() User {
	return User{ID: u.ID, Username: u.Username, SecretKeyHash: u.SecretKeyHash, DisplayName: u.DisplayName,
		Bio: u.Bio, ProfileImageID: u.ProfileImageID, Region: u.Region, AccountType: u.AccountType, Status: u.Status}
}

// syncPeers are the other regions' apps, from ISUREAL_PEERS (comma separated
// base URLs of their port 8080). Without peers the hot state is local only.
var syncPeers = func() []string {
	var peers []string
	for _, peer := range strings.Split(os.Getenv("ISUREAL_PEERS"), ",") {
		if peer = strings.TrimSpace(peer); peer != "" {
			peers = append(peers, strings.TrimRight(peer, "/"))
		}
	}
	return peers
}()

var syncClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 128, IdleConnTimeout: 5 * time.Minute},
}

// syncToken authenticates internal requests between the apps, which share the
// database password.
var syncToken = func() string {
	sum := sha256.Sum256([]byte("isureal-sync:" + os.Getenv("ISUREAL_DB_PASSWORD")))
	return hex.EncodeToString(sum[:])
}()

// broadcast applies events here and on every peer, and returns when all have
// applied them. Call it after the change committed and before responding.
func broadcast(ctx context.Context, events ...stateEvent) {
	if len(events) == 0 {
		return
	}
	hot.apply(events)
	sendToPeers(ctx, "/internal/state-events", events)
}

func sendToPeers(ctx context.Context, path string, body any) {
	if len(syncPeers) == 0 {
		return
	}
	payload, err := json.Marshal(body)
	if err != nil {
		slog.Error("sync marshal", "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, peer := range syncPeers {
		wg.Go(func() {
			// The change has committed; finish delivering it even if the client
			// went away, or the peer would serve stale reads.
			ctx := context.WithoutCancel(ctx)
			for attempt := 0; attempt < 3; attempt++ {
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+path, bytes.NewReader(payload))
				if err != nil {
					return
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Isureal-Sync", syncToken)
				response, err := syncClient.Do(request)
				if err == nil {
					response.Body.Close()
					if response.StatusCode == http.StatusNoContent {
						return
					}
					err = fmt.Errorf("status %d", response.StatusCode)
				}
				slog.Error("sync to peer failed", "peer", peer, "path", path, "error", err)
			}
		})
	}
	wg.Wait()
}

func registerSyncHandlers(e *echo.Echo, h *HandlerContext) {
	e.POST("/internal/state-events", func(c *echo.Context) error {
		if c.Request().Header.Get("X-Isureal-Sync") != syncToken {
			return c.NoContent(http.StatusForbidden)
		}
		var events []stateEvent
		if err := json.NewDecoder(c.Request().Body).Decode(&events); err != nil {
			return c.NoContent(http.StatusBadRequest)
		}
		hot.apply(events)
		return c.NoContent(http.StatusNoContent)
	})
	e.POST("/internal/state-reload", func(c *echo.Context) error {
		if c.Request().Header.Get("X-Isureal-Sync") != syncToken {
			return c.NoContent(http.StatusForbidden)
		}
		if err := hot.load(c.Request().Context(), h.DB); err != nil {
			return err
		}
		warmConnections(c.Request().Context(), h.DB)
		return c.NoContent(http.StatusNoContent)
	})
}

// reloadEverywhere reloads the hot state here and on every peer (after the
// database was reset).
func reloadEverywhere(ctx context.Context, db *sqlx.DB) error {
	if err := hot.load(ctx, db); err != nil {
		return err
	}
	sendToPeers(ctx, "/internal/state-reload", struct{}{})
	return nil
}

// realActor resolves the deferred actor marker of a batched session.
func realActor(ctx context.Context, actor string) string {
	if state, _ := ctx.Value(batchSessionKey{}).(*batchSession); state != nil && actor == state.marker {
		return state.session.UserID
	}
	return actor
}

func argString(args any, key string) string {
	if m, ok := args.(map[string]any); ok {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

// eventsFor describes the hot state changes of a committed optimized_write.
func eventsFor(ctx context.Context, db *sqlx.DB, action, actor string, args any, result writeResult, version int64) []stateEvent {
	actor = realActor(ctx, actor)
	target := argString(args, "target")
	switch action {
	case "register", "profile_update":
		return []stateEvent{{Version: version, Kind: "user", User: newEventUser(result.User)}}
	case "login":
		return []stateEvent{{Version: version, Kind: "session", A: result.User.ID, B: argString(args, "tokenHash")}}
	case "logout":
		return []stateEvent{{Version: version, Kind: "session", A: actor}}
	case "account_delete":
		return []stateEvent{{Version: version, Kind: "user_deleted", A: actor}}
	case "profile_image_put", "profile_image_delete":
		if event, ok := refreshUserEvent(ctx, db, actor); ok {
			return []stateEvent{event}
		}
	case "post_create", "post_update":
		post := result.Post
		return []stateEvent{
			{Version: version, Kind: "user", User: newEventUser(result.User)},
			{Version: version, Kind: "post", Post: &post},
		}
	case "post_delete":
		return []stateEvent{{Version: version, Kind: "post_deleted", A: argString(args, "id")}}
	case "friend_create", "friend_transition":
		if r := result.Request; r.Status == "accepted" {
			return []stateEvent{{Version: version, Kind: "friend", A: r.RequesterID, B: r.RecipientID, Exists: true}}
		}
	case "friend_delete":
		return []stateEvent{{Version: version, Kind: "friend", A: actor, B: target}}
	case "follow", "unfollow":
		return []stateEvent{{Version: version, Kind: "follow", A: actor, B: target, Exists: action == "follow"}}
	case "block":
		return []stateEvent{
			{Version: version, Kind: "block", A: actor, B: target, Exists: true},
			{Version: version, Kind: "friend", A: actor, B: target},
			{Version: version, Kind: "follow", A: actor, B: target},
			{Version: version, Kind: "follow", A: target, B: actor},
		}
	case "unblock":
		return []stateEvent{{Version: version, Kind: "block", A: actor, B: target}}
	}
	return nil
}

// refreshUserEvent reads a user with a fresh version while holding the row
// lock, so no other write to the user can interleave between the two.
func refreshUserEvent(ctx context.Context, db *sqlx.DB, userID string) (stateEvent, bool) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return stateEvent{}, false
	}
	defer tx.Rollback()
	var u User
	if err := tx.GetContext(ctx, &u, `SELECT `+userProjection+` FROM users WHERE id = $1 FOR SHARE`, userID); err != nil {
		return stateEvent{}, false
	}
	var version int64
	if err := tx.GetContext(ctx, &version, `SELECT nextval('change_seq')`); err != nil {
		return stateEvent{}, false
	}
	if err := tx.Commit(); err != nil {
		return stateEvent{}, false
	}
	return stateEvent{Version: version, Kind: "user", User: newEventUser(u)}, true
}
