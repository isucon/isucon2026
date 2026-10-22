package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestAccountSyncScheduleSurvivesRestartAndConcurrentWorkers(t *testing.T) {
	p := testkit.Start(t)
	p.CreateTeam(t, "active")
	team, _ := p.CreateTeam(t, "disqualified")
	if _, err := p.DB.Exec(`UPDATE teams SET disqualified=true WHERE id=$1`, team); err != nil {
		t.Fatal(err)
	}
	_, unlinked := p.CreateTeam(t, "unlinked")
	if _, err := p.DB.Exec(`UPDATE contestants SET github_login='', discord_id='' WHERE id=$1`, unlinked); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := &job.DBQueue{DB: p.DB}
			if err := q.ScheduleAccountSync(ctx, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := p.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("scheduled %d jobs, want 3 active + 2 disqualified Discord jobs", count)
	}
	var spread bool
	if err := p.DB.QueryRow(`SELECT min(run_at) < max(run_at) AND max(run_at) < now()+interval '1 hour' FROM jobs`).Scan(&spread); err != nil {
		t.Fatal(err)
	}
	if !spread {
		t.Fatal("jobs are not distributed over the hour")
	}
	// A fresh worker must keep the durable next-run time, even after jobs complete.
	if _, err := p.DB.Exec(`DELETE FROM jobs WHERE name='sync_ssh_keys_of_contestant'`); err != nil {
		t.Fatal(err)
	}
	q := &job.DBQueue{DB: p.DB}
	if err := q.ScheduleAccountSync(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := p.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("restart rescheduled completed jobs: %d", count)
	}
	if _, err := p.DB.Exec(`UPDATE periodic_job_schedules SET next_run_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := q.ScheduleAccountSync(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := p.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("next cycle should requeue only completed work: %d", count)
	}
}

func TestAccountSyncSchedulePerformsKeysAndRolesIndependently(t *testing.T) {
	p := testkit.Start(t)
	_, id := p.CreateTeam(t, "sync")
	if _, err := p.DB.Exec(`INSERT INTO ssh_public_keys(contestant_id,public_key,created_at,updated_at) VALUES($1,'old-key',now(),now())`, id); err != nil {
		t.Fatal(err)
	}
	failKeys := true
	roleAdded := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sync-gh.keys":
			if failKeys {
				http.Error(w, "unavailable", 503)
				return
			}
			fmt.Fprintln(w, "ssh-ed25519 new-key")
		case r.Method == "GET" && r.URL.Path == "/guilds/guild/members/dc-sync":
			fmt.Fprint(w, `{"roles":[]}`)
		case r.Method == "PUT" && r.URL.Path == "/guilds/guild/members/dc-sync/roles/contestant":
			roleAdded = true
			w.WriteHeader(204)
		case r.Method == "PATCH":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	p.Jobs.GitHub.KeysBaseURL = api.URL
	p.Jobs.Discord.BotToken = "test"
	p.Jobs.Discord.ServerID = "guild"
	p.Jobs.Discord.APIBaseURL = api.URL
	p.Cfg.Discord.ContestantRoleID = "contestant"
	q := &job.DBQueue{DB: p.DB}
	ctx := context.Background()
	if err := q.ScheduleAccountSync(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec(`UPDATE jobs SET run_at=now()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if worked, err := q.Work(ctx, p.Jobs); err != nil || !worked {
			t.Fatalf("work: %v %v", worked, err)
		}
	}
	var member bool
	if err := p.DB.QueryRow(`SELECT is_discord_guild_member FROM contestants WHERE id=$1`, id).Scan(&member); err != nil {
		t.Fatal(err)
	}
	if !member || !roleAdded {
		t.Fatalf("Discord sync blocked by GitHub failure: member=%v role=%v", member, roleAdded)
	}
	var key string
	if err := p.DB.QueryRow(`SELECT public_key FROM ssh_public_keys WHERE contestant_id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "old-key" {
		t.Fatalf("failed fetch removed existing key: %s", key)
	}
	failKeys = false
	if _, err := p.DB.Exec(`UPDATE jobs SET run_at=now()`); err != nil {
		t.Fatal(err)
	}
	if worked, err := q.Work(ctx, p.Jobs); err != nil || !worked {
		t.Fatalf("retry: %v %v", worked, err)
	}
	if err := p.DB.QueryRow(`SELECT public_key FROM ssh_public_keys WHERE contestant_id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "ssh-ed25519 new-key" {
		t.Fatalf("key was not refreshed: %s", key)
	}
}

func TestAccountSyncWithoutBotSkipsDiscord(t *testing.T) {
	p := testkit.Start(t)
	p.CreateTeam(t, "no-bot")
	q := &job.DBQueue{DB: p.DB}
	if err := q.ScheduleAccountSync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := p.DB.QueryRow(`SELECT name FROM jobs`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || name != "sync_ssh_keys_of_contestant" {
		t.Fatalf("unexpected jobs: %d %s", count, name)
	}
}
