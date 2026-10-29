package isureal

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestWriteRoundtripCounts(t *testing.T) {
	db, counter := tracedTestDB(t)
	seedRoundtripFixture(t, db)
	if _, err := db.Exec(`UPDATE users SET account_type='certified_public' WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	owner, err := CreateLoginSession(db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	h := &HandlerContext{DB: db, Config: &Config{Region: "asia"}}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	auth := BatchSessionMiddleware(h, true)
	e.POST("/register", registerHandler(h))
	e.POST("/login", loginHandler(h))
	e.POST("/logout", logoutHandler(h), auth)
	e.PATCH("/me", updateMeHandler(h), auth)
	e.PUT("/profile", putProfileImageHandler(h), auth)
	e.DELETE("/profile", deleteProfileImageHandler(h), auth)
	e.POST("/posts", createPostHandler(h), auth)
	e.PATCH("/posts/:postId", updatePostHandler(h), auth)
	e.DELETE("/posts/:postId", deletePostHandler(h), auth)
	e.POST("/posts/:postId/comments", createCommentHandler(h), auth)
	e.DELETE("/comments/:commentId", deleteCommentHandler(h), auth)
	e.POST("/posts/:postId/realmojis", replaceRealMojiHandler(h), auth)
	e.DELETE("/realmojis/:realMojiId", deleteRealMojiHandler(h), auth)
	e.PUT("/follows/:userId", followHandler(h), auth)
	e.DELETE("/follows/:userId", unfollowHandler(h), auth)
	e.POST("/requests", createFriendRequestHandler(h), auth)
	e.POST("/requests/:requestId/:action", transitionFriendRequestHandler(h), auth)
	e.DELETE("/friends/:userId", deleteFriendshipHandler(h), auth)
	e.PUT("/blocks/:userId", blockHandler(h), auth)
	e.DELETE("/blocks/:userId", unblockHandler(h), auth)
	e.POST("/delete-account", deleteAccountHandler(h), auth)
	request := func(method, path, token, body, media string, status int, queries int64) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", media)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		counter.count.Store(0)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		if n := counter.count.Load(); n != queries {
			t.Fatalf("%s %s: %d queries, expected %d", method, path, n, queries)
		}
		return rec
	}
	var registered registerResponse
	rec := request("POST", "/register", "", `{"username":"new","displayName":"New","region":"asia"}`, "application/json", 201, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	loginBody, _ := json.Marshal(loginRequest{Username: "new", SecretKey: registered.SecretKey})
	var login loginResponse
	rec = request("POST", "/login", "", string(loginBody), "application/json", 200, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	token := login.Token
	request("PATCH", "/me", token, `{"displayName":"changed","bio":"bio"}`, "application/json", 200, 1)
	// Profile image changes read the user again (in a short transaction with a
	// fresh version) for the hot state event: 1 write + 4 for the refresh.
	request("PUT", "/profile", token, string(testJPEG(t)), "image/jpeg", 204, 5)
	request("DELETE", "/profile", token, "", "", 204, 5)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range map[string]string{"visibility": "public", "location_kind": "none", "caption": "post", "reactionType": "like"} {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("image", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(testJPEG(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	rec = request("POST", "/posts", token, body.String(), writer.FormDataContentType(), 201, 1)
	var post createPostResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &post); err != nil {
		t.Fatal(err)
	}
	request("PATCH", "/posts/"+post.ID, token, `{"caption":"edited","visibility":"friends_of_friends"}`, "application/json", 200, 1)
	var comment createCommentResponse
	rec = request("POST", "/posts/post/comments", token, `{"body":"comment"}`, "application/json", 201, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &comment); err != nil {
		t.Fatal(err)
	}
	request("DELETE", "/comments/"+comment.ID, token, "", "", 204, 1)
	var reaction replaceRealMojiResponse
	rec = request("POST", "/posts/post/realmojis", token, body.String(), writer.FormDataContentType(), 201, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &reaction); err != nil {
		t.Fatal(err)
	}
	request("DELETE", "/realmojis/"+reaction.ID, token, "", "", 204, 1)
	request("PUT", "/follows/owner", token, "", "", 204, 1)
	request("DELETE", "/follows/owner", token, "", "", 204, 1)
	var relation createFriendRequestResponse
	rec = request("POST", "/requests", token, `{"recipientId":"owner"}`, "application/json", 201, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &relation); err != nil {
		t.Fatal(err)
	}
	request("POST", "/requests/"+relation.RelationID+"/accept", owner.Token, "", "", 200, 1)
	request("DELETE", "/friends/owner", token, "", "", 204, 1)
	request("PUT", "/blocks/owner", token, "", "", 204, 1)
	request("DELETE", "/blocks/owner", token, "", "", 204, 1)
	request("DELETE", "/posts/"+post.ID, token, "", "", 204, 1)
	request("POST", "/logout", token, "", "", 204, 1)
	rec = request("POST", "/login", "", string(loginBody), "application/json", 200, 1)
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	request("POST", "/delete-account", login.Token, "", "", 204, 1)
	request("PATCH", "/me", login.Token, `{"bio":"denied"}`, "application/json", http.StatusUnauthorized, 1)
}
