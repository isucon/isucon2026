package registrationmigration

import (
	"os"
	"strings"
	"testing"
	"time"
)

func snapshot() Snapshot {
	return Snapshot{Version: 1, Users: []User{{ID: 10, TeamID: ptr(int64(20)), Username: "alice", DisplayName: "Alice", GitHubID: "123", IsActive: true, Icon: "icons/alice.png"}}, Teams: []Team{{ID: 20, Owner: 10, Name: "team", IsActive: true, AWSAZ: "apne1-az1", CreatedAt: time.Now(), UpdatedAt: time.Now()}}, Other: EmptyInventory(), Avatars: map[string]string{"icons/alice.png": "https://cdn.example.invalid/alice.png"}}
}
func ptr[T any](v T) *T { return &v }
func TestValidate(t *testing.T) {
	if err := snapshot().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Snapshot){
		"disabled user":      func(s *Snapshot) { s.Users[0].IsActive = false },
		"missing timestamp":  func(s *Snapshot) { s.Teams[0].CreatedAt = time.Time{} },
		"ambiguous coupon":   func(s *Snapshot) { s.Teams[0].AWSCoupon = "a,b" },
		"missing inventory":  func(s *Snapshot) { delete(s.Other, "contact.Ticket") },
		"missing github":     func(s *Snapshot) { s.Users[0].GitHubID = "" },
		"nonnumeric github":  func(s *Snapshot) { s.Users[0].GitHubID = "abc" },
		"invalid owner":      func(s *Snapshot) { s.Teams[0].Owner = 999 },
		"invalid membership": func(s *Snapshot) { s.Users[0].TeamID = ptr(int64(999)) },
		"duplicate github":   func(s *Snapshot) { u := s.Users[0]; u.ID = 11; s.Users = append(s.Users, u) },
		"duplicate discord": func(s *Snapshot) {
			s.Users[0].DiscordID = "456"
			u := s.Users[0]
			u.ID = 11
			u.GitHubID = "124"
			s.Users = append(s.Users, u)
		},
		"unsupported tickets": func(s *Snapshot) { s.Other["contact.Ticket"] = 1 },
		"expired avatar": func(s *Snapshot) {
			s.Avatars["icons/alice.png"] = "https://cdn.example.invalid/a?Expires=1&Signature=x"
		},
		"missing avatar": func(s *Snapshot) { delete(s.Avatars, "icons/alice.png") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := snapshot()
			mutate(&s)
			if s.Validate() == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	s := snapshot()
	u := s.Users[0]
	u.ID = 11
	u.GitHubID = "124"
	s.Users = append(s.Users, u)
	if err := s.Validate(); err != nil {
		t.Fatalf("unlinked discord: %v", err)
	}
}
func TestDecodeRejectsUnknownFields(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"version":1,"password":"secret"}`)); err == nil {
		t.Fatal("unknown input accepted")
	}
}

func TestDecodeSyntheticSnapshot(t *testing.T) {
	file, err := os.Open("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	s, err := Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	user := s.Users[0]
	if user.TermsAgreedAt.Nanosecond() != 123456000 || user.PrivacyAgreedAt != nil || user.MinorParentalConsent == nil || *user.MinorParentalConsent || user.Email != "alice@example.invalid" {
		t.Fatalf("snapshot values lost: %+v", user)
	}
}

func TestRegistrationCoupons(t *testing.T) {
	s := snapshot()
	s.RegistrationCoupons = []RegistrationCoupon{{ID: 1, Name: "invitation", Token: "synthetic-token", TeamID: ptr(int64(20)), CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	s.Other["authentication.RegisterCoupon"] = 1
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Snapshot){
		"missing records":   func(s *Snapshot) { s.RegistrationCoupons = nil },
		"missing inventory": func(s *Snapshot) { s.Other["authentication.RegisterCoupon"] = 0 },
		"unknown team":      func(s *Snapshot) { s.RegistrationCoupons[0].TeamID = ptr(int64(999)) },
		"duplicate": func(s *Snapshot) {
			s.RegistrationCoupons = append(s.RegistrationCoupons, s.RegistrationCoupons[0])
			s.Other["authentication.RegisterCoupon"] = 2
		},
		"timestamp": func(s *Snapshot) { s.RegistrationCoupons[0].CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := s
			changed.Other = EmptyInventory()
			changed.Other["authentication.RegisterCoupon"] = 1
			changed.RegistrationCoupons = append([]RegistrationCoupon(nil), s.RegistrationCoupons...)
			mutate(&changed)
			if changed.Validate() == nil {
				t.Fatal("invalid coupons accepted")
			}
		})
	}
}
