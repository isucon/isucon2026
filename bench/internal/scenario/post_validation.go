package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
)

// ValidationSnapshot is private evidence stored by the benchmark operator, never
// by the application under test. Credentials permit new sessions after a reboot.
type ValidationSnapshot struct {
	Target  string           `json:"target"`
	Host    string           `json:"host"`
	Version int              `json:"version"`
	Users   []ValidationUser `json:"users"`
}
type ValidationUser struct {
	ID        string          `json:"id"`
	Username  string          `json:"username"`
	SecretKey string          `json:"secret_key"`
	Region    string          `json:"region"`
	Country   string          `json:"country"`
	Months    []string        `json:"months"`
	State     json.RawMessage `json:"state"`
}

func captureValidationUser(ctx context.Context, a *agent.Agent, months []string) (ValidationUser, error) {
	state, err := validationState(ctx, a, months)
	return ValidationUser{ID: a.ID, Username: a.Username, SecretKey: a.SecretKey, Region: a.Region, Country: a.Country(), Months: months, State: state}, err
}

func ValidateSnapshot(ctx context.Context, api *client.Client, saved ValidationSnapshot) error {
	if saved.Version != 1 || len(saved.Users) == 0 || len(saved.Users) > 12 {
		return errors.New("invalid or empty post-validation evidence")
	}
	for _, u := range saved.Users {
		if u.ID == "" || u.Username == "" || u.SecretKey == "" || len(u.Months) == 0 || len(u.State) == 0 {
			return errors.New("incomplete post-validation evidence")
		}
		a := &agent.Agent{ID: u.ID, Username: u.Username, SecretKey: u.SecretKey, Region: u.Region, API: api.WithCountry(u.Country)}
		if err := a.Login(ctx); err != nil {
			return fmt.Errorf("%s: login after restart: %w", u.Username, err)
		}
		got, err := validationState(ctx, a, u.Months)
		if err != nil {
			return fmt.Errorf("%s: %w", u.Username, err)
		}
		var expected any
		if err := json.Unmarshal(u.State, &expected); err != nil {
			return err
		}
		canonical, err := json.Marshal(expected)
		if err != nil {
			return err
		}
		if string(got) != string(canonical) {
			return fmt.Errorf("%s: persisted profile, posts, images or relationships changed after restart", u.Username)
		}
	}
	return nil
}

func validationState(ctx context.Context, a *agent.Agent, months []string) (json.RawMessage, error) {
	profile, err := a.API.Me(ctx, a.Token)
	if err != nil {
		return nil, err
	}
	posts := []client.MemoryPost{}
	for _, month := range months {
		var before *client.Post
		for pages := 0; ; pages++ {
			if pages >= 1000 {
				return nil, errors.New("memories pagination exceeded limit")
			}
			page, err := a.API.MemoriesMonth(ctx, a.Token, month, before)
			if err != nil {
				return nil, err
			}
			posts = append(posts, page.Items...)
			if !page.HasMore {
				break
			}
			if len(page.Items) == 0 {
				return nil, errors.New("empty non-final memories page")
			}
			before = &page.Items[len(page.Items)-1].Post
		}
	}
	friends := []client.Relationship{}
	var before *client.Relationship
	for pages := 0; ; pages++ {
		if pages >= 1000 {
			return nil, errors.New("relationships pagination exceeded limit")
		}
		page, err := a.API.Relationships(ctx, a.Token, "friends", before)
		if err != nil {
			return nil, err
		}
		friends = append(friends, page.Items...)
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return nil, errors.New("empty non-final relationships page")
		}
		before = &page.Items[len(page.Items)-1]
	}
	data, err := json.Marshal(map[string]any{"profile": profile, "posts": posts, "friends": friends})
	if err != nil {
		return nil, err
	}
	var value any
	if err = json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	// Signed image URLs expire and change after a reboot. Compare the actual image
	// bytes instead; sort collections to avoid depending on unspecified tie order.
	if err = normalizeValidation(ctx, a.API, value, map[string]string{}); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func normalizeValidation(ctx context.Context, api *client.Client, value any, images map[string]string) error {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if url, ok := x.(string); ok && strings.HasSuffix(strings.ToLower(k), "imageurl") && url != "" {
				digest, ok := images[url]
				if !ok {
					data, err := api.ImageBytes(ctx, url)
					if err != nil {
						return err
					}
					sum := sha256.Sum256(data)
					digest = hex.EncodeToString(sum[:])
					images[url] = digest
				}
				v[k] = digest
			} else if err := normalizeValidation(ctx, api, x, images); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range v {
			if err := normalizeValidation(ctx, api, x, images); err != nil {
				return err
			}
		}
		sort.Slice(v, func(i, j int) bool {
			a, _ := json.Marshal(v[i])
			b, _ := json.Marshal(v[j])
			return string(a) < string(b)
		})
	}
	return nil
}

func (r *Runner) validationSnapshot(ctx context.Context) (ValidationSnapshot, error) {
	snapshot := ValidationSnapshot{Version: 1}
	months := []string{}
	for _, m := range r.model.LoadMonths() {
		months = append(months, m.Format("2006-01"))
	}
	counts := map[string]int{}
	for _, u := range r.usersSnapshot() {
		if !u.registered || u.stopped.Load() || u.Token == "" || counts[u.Region] >= 3 {
			continue
		}
		// Verify accepted writes against the model before recording what the server
		// returned. A server's pre-restart response is not itself a correctness oracle.
		if err := r.finalCheckUser(ctx, u.Agent, r.model.LoadMonths()); err != nil {
			return snapshot, err
		}
		saved, err := captureValidationUser(ctx, u.Agent, months)
		if err != nil {
			return snapshot, err
		}
		snapshot.Users = append(snapshot.Users, saved)
		counts[u.Region]++
	}
	if len(counts) != 3 || len(months) == 0 {
		return snapshot, errors.New("insufficient evidence from all three regions")
	}
	return snapshot, nil
}

func (r *Runner) SetValidationSaver(save func(context.Context, ValidationSnapshot) error) {
	r.cfg.SaveValidation = save
}
