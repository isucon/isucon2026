package model

import (
	"strings"
	"testing"
)

// 友達とフォローの一覧は、全ページを通して同じ相手が 2 回現れれば誤りとする（ページの境目で
// 前のページの末尾がまた返るなど）。
func TestRelationListDuplicates(t *testing.T) {
	w := newWorld(t)
	m := w.m
	m.BeginFollow("a", "b", true).Applied()
	r := w.later()
	// b の友達は a と c。
	if v := m.CheckFriends("b", []string{"a", "a", "c"}, r); len(v) != 1 || !strings.Contains(v[0], "重複") {
		t.Fatalf("duplicated friend: %v", v)
	}
	if v := m.CheckFollowing("a", []string{"b", "b"}, r); len(v) != 1 || !strings.Contains(v[0], "重複") {
		t.Fatalf("duplicated followee: %v", v)
	}
	if v := m.CheckFollowers("b", []string{"a", "a"}, r); len(v) != 1 || !strings.Contains(v[0], "重複") {
		t.Fatalf("duplicated follower: %v", v)
	}
	// 重複していても、欠けている友達は別に見つける。
	if v := m.CheckFriends("b", []string{"a", "a"}, r); len(v) != 2 {
		t.Fatalf("duplicated and missing friends: %v", v)
	}
}

// フォローの一覧に bench の知らないユーザーがいれば、存在しないフォローとして誤りとする。
func TestFollowingWithUnknownUser(t *testing.T) {
	w := newWorld(t)
	m := w.m
	m.BeginFollow("a", "b", true).Applied()
	r := w.later()
	if v := m.CheckFollowing("a", []string{"b"}, r); len(v) != 0 {
		t.Fatalf("consistent following: %v", v)
	}
	if v := m.CheckFollowing("a", []string{"b", "ghost"}, r); len(v) != 1 || !strings.Contains(v[0], "ghost") {
		t.Fatalf("unknown followee: %v", v)
	}
}

// フォロワーの一覧は、確実にフォローしている人の欠落と、フォローしていない人を誤りとする。
// 結果のわからないフォローと解除は、あってもなくてもよい。
func TestCheckFollowers(t *testing.T) {
	w := newWorld(t)
	m := w.m
	m.BeginFollow("a", "b", true).Applied()
	m.BeginFollow("c", "b", true).Unknown()
	m.BeginFollow("e", "b", true).Applied()
	m.BeginDeleteAccount("e").Applied()
	r := w.later()
	for _, got := range [][]string{{"a"}, {"a", "c"}, {"c", "a"}} {
		if v := m.CheckFollowers("b", got, r); len(v) != 0 {
			t.Fatalf("followers %v: %v", got, v)
		}
	}
	if v := m.CheckFollowers("b", nil, r); len(v) != 1 || !strings.Contains(v[0], "いません") {
		t.Fatalf("missing follower: %v", v)
	}
	for _, extra := range []string{"d", "e", "ghost"} {
		if v := m.CheckFollowers("b", []string{"a", extra}, r); len(v) != 1 || !strings.Contains(v[0], extra) {
			t.Fatalf("extra follower %s: %v", extra, v)
		}
	}
	m.BeginFollow("a", "b", false).Unknown()
	if v := m.CheckFollowers("b", nil, w.later()); len(v) != 0 {
		t.Fatalf("follower whose unfollow is unknown: %v", v)
	}
}
