package isureal

import "testing"

// Sessions reach other regions late, so the hot state vouches only for the
// sessions of this region's users; the others are checked in the database.
func TestHotSessionsOnlyForHomeRegion(t *testing.T) {
	saved := homeRegion
	defer func() {
		homeRegion = saved
		hot.mu.Lock()
		hot.reset()
		hot.mu.Unlock()
	}()
	hot.mu.Lock()
	hot.reset()
	hot.setUser(newHotUser(User{ID: "u", Username: "u", Region: "asia", Status: "active"}, 1))
	session := hotSession{UserID: "u", TokenHash: "token", Version: 1}
	hot.sessions["token"] = session
	hot.userSession["u"] = session
	hot.loaded = true
	hot.mu.Unlock()

	homeRegion = "asia"
	if _, ok := hot.authenticate("token", ""); !ok {
		t.Fatal("the home region does not trust its user's session")
	}
	homeRegion = "europe"
	if _, ok := hot.authenticate("token", ""); ok {
		t.Fatal("another region trusts the session from its hot state")
	}
	if _, ok := hot.authenticate("token", "asia"); ok {
		t.Fatal("another region trusts the session for the user's region")
	}
}
