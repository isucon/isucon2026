package isureal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestSignedImagesIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	config := &Config{Region: "asia", DBHost: u.Hostname(), DBPort: u.Port(), DBUser: u.User.Username(), DBPassword: password, DBName: strings.TrimPrefix(u.Path, "/")}
	e := NewServer(config)
	request := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	rec := request(http.MethodPost, "/api/auth/register", "", `{"username":"owner","displayName":"Owner","region":"asia"}`, http.StatusCreated)
	var registration registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &registration); err != nil {
		t.Fatal(err)
	}
	ownerID := registration.User.ID
	key := sha256.Sum256([]byte(registration.SecretKey))
	loginBody, err := json.Marshal(loginRequest{Username: "owner", SecretKey: registration.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	rec = request(http.MethodPost, "/api/auth/login", "", string(loginBody), http.StatusOK)
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	reactorKey := sha256.Sum256([]byte("reactor-secret"))
	exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES('reactor','reactor','Reactor',$1,'europe')`, hex.EncodeToString(reactorKey[:]))
	exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES('profile',$1,'profile','image/jpeg'),('photo',$1,'photo','image/jpeg'),('reaction','reactor','reaction','image/jpeg')`, ownerID)
	exec(`UPDATE users SET profile_image_id='profile' WHERE id=$1`, ownerID)
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('w','asia',CURRENT_DATE,clock_timestamp(),clock_timestamp()+interval '1 day')`)
	exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES('post',$1,'w','photo','friends','none',clock_timestamp())`, ownerID)
	exec(`INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES('r','post','reactor','reaction','like',clock_timestamp())`)

	before := time.Now().Unix()
	rec = request(http.MethodGet, "/api/me", login.Token, "", http.StatusOK)
	var me getMeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.ProfileImageURL == nil {
		t.Fatalf("profile response: %s (%v)", rec.Body.String(), err)
	}
	rec = request(http.MethodGet, "/api/posts/post", login.Token, "", http.StatusOK)
	var post getPostResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &post); err != nil {
		t.Fatal(err)
	}
	rec = request(http.MethodGet, "/api/posts/post/realmojis", login.Token, "", http.StatusOK)
	var reactions getRealMojisResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reactions); err != nil || len(reactions.Items) != 1 {
		t.Fatalf("reaction response: %s (%v)", rec.Body.String(), err)
	}
	links := []struct {
		url     string
		imageID string
		key     []byte
	}{
		{*me.ProfileImageURL, "profile", key[:]},
		{post.ImageURL, "photo", key[:]},
		{reactions.Items[0].ImageURL, "reaction", reactorKey[:]},
	}
	for _, link := range links {
		parsed, err := url.Parse(link.url)
		if err != nil {
			t.Fatal(err)
		}
		exp, err := strconv.ParseInt(parsed.Query().Get("exp"), 10, 64)
		if err != nil || exp < before+86400 || exp > time.Now().Unix()+86400 {
			t.Fatalf("expiry: %s", link.url)
		}
		want := base64.RawURLEncoding.EncodeToString(imageSignature(link.key, link.imageID, exp))
		if parsed.Query().Get("sig") != want {
			t.Fatalf("not signed by image owner: %s", link.url)
		}
		for _, region := range []string{"asia", "europe", "america"} {
			config.Region = region
			rec = request(http.MethodGet, link.url, "", "", http.StatusOK)
			if rec.Body.String() != link.imageID || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("image response: %v %s", rec.Header(), rec.Body.String())
			}
		}
		request(http.MethodGet, "/api/images/"+link.imageID, login.Token, "", http.StatusForbidden)
		query := parsed.Query()
		query.Set("exp", strconv.FormatInt(exp+1, 10))
		request(http.MethodGet, parsed.Path+"?"+query.Encode(), "", "", http.StatusForbidden)
		query = parsed.Query()
		query.Set("sig", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
		request(http.MethodGet, parsed.Path+"?"+query.Encode(), "", "", http.StatusForbidden)
		request(http.MethodGet, "/api/images/missing?"+parsed.RawQuery, "", "", http.StatusForbidden)
	}
	parsed, _ := url.Parse(links[0].url)
	request(http.MethodGet, "/api/images/photo?"+parsed.RawQuery, "", "", http.StatusForbidden)
	config.Region = "asia"
	request(http.MethodPatch, "/api/me", login.Token, `{"region":"europe"}`, http.StatusOK)
	request(http.MethodPost, "/api/auth/logout", login.Token, "", http.StatusNoContent)
	for _, link := range links {
		request(http.MethodGet, link.url, "", "", http.StatusOK)
		request(http.MethodGet, link.url, "invalid-session", "", http.StatusOK)
	}

	// 削除・退会・差し替えの直後から、全地域で取得できなくなることを確認する。
	for _, transition := range []struct {
		name   string
		update string
		reset  string
		denied []int
	}{
		{"reactor retirement", `UPDATE users SET status='deleted' WHERE id='reactor'`, `UPDATE users SET status='active' WHERE id='reactor'`, []int{2}},
		{"reaction replacement", `UPDATE realmojis SET replaced_at=clock_timestamp() WHERE id='r'`, `UPDATE realmojis SET replaced_at=NULL WHERE id='r'`, []int{2}},
		{"reaction deletion", `UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id='r'`, `UPDATE realmojis SET deleted_at=NULL WHERE id='r'`, []int{2}},
		{"post deletion", `UPDATE posts SET deleted_at=clock_timestamp() WHERE id='post'`, `UPDATE posts SET deleted_at=NULL WHERE id='post'`, []int{1, 2}},
		{"author retirement", `UPDATE users SET status='deleted' WHERE username='owner'`, `UPDATE users SET status='active' WHERE username='owner'`, []int{0, 1, 2}},
	} {
		t.Run(transition.name, func(t *testing.T) {
			exec(transition.update)
			defer exec(transition.reset)
			for _, region := range []string{"asia", "europe", "america"} {
				config.Region = region
				for _, index := range transition.denied {
					request(http.MethodGet, links[index].url, "", "", http.StatusForbidden)
				}
			}
		})
	}
}
