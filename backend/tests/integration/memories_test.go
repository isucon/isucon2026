package integration_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"testing"
	"time"
)

func TestUS4MemoriesMonthDateAndOwnership(t *testing.T) {
	pool, posts := us1Database(t)
	owner, other := uuid.New(), uuid.New()
	addUser(t, pool, owner, "america")
	addUser(t, pool, other, "america")
	firstWindow := uuid.New()
	addWindow(t, pool, firstWindow, "america", false)
	addFriendship(t, pool, owner, other)
	createPost(t, posts, other, "other", domain.VisibilityFriends, us1Now)
	first := createPost(t, posts, owner, "first", domain.VisibilityPublic, us1Now)
	interactions := postgres.NewInteractionRepository(&postgres.DB{Pool: pool})
	if _, err := interactions.ReplaceRealMoji(context.Background(), other, first.Post.ID, interactionObject(other), domain.ReactionLike, uuid.New(), us1Now); err != nil {
		t.Fatal(err)
	}
	if _, err := interactions.CreateComment(context.Background(), other, first.Post.ID, uuid.New(), "remembered", us1Now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE posting_windows SET ended_at=$2 WHERE id=$1`, firstWindow, us1Now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	secondWindow := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline)VALUES($1,'america','2026-07-20',-300,$2,$3)`, secondWindow, us1Now.Add(24*time.Hour), us1Now.Add(24*time.Hour+2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	second := createPost(t, posts, owner, "second", domain.VisibilityPublic, us1Now.Add(24*time.Hour))
	repository := postgres.NewMemoryRepository(&postgres.DB{Pool: pool})
	month, err := repository.ListMonth(context.Background(), owner, "2026-07", nil, nil, 51)
	if err != nil || len(month) != 2 || month[0].PostRecord.Post.ID != second.Post.ID {
		t.Fatalf("month=%#v err=%v", month, err)
	}
	date, err := repository.ListDate(context.Background(), owner, "2026-07-19", nil, nil, 51)
	if err != nil || len(date) != 1 || date[0].PostRecord.Post.ID != first.Post.ID {
		t.Fatalf("date=%#v err=%v", date, err)
	}
	if len(date[0].RealMojis) != 1 || len(date[0].Comments) != 1 {
		t.Fatal("owner memory interaction snapshot missing")
	}
	others, err := repository.ListMonth(context.Background(), other, "2026-07", nil, nil, 51)
	if err != nil || len(others) != 1 || others[0].PostRecord.Post.AuthorID != other {
		t.Fatalf("other memories=%d err=%v", len(others), err)
	}
	caption := "denied"
	if _, err := posts.Update(context.Background(), owner, first.Post.ID, &caption, nil); !errors.Is(err, postgres.ErrPostNotEditable) {
		t.Fatalf("ended update=%v", err)
	}
	if _, err := posts.Delete(context.Background(), other, first.Post.ID, us1Now.Add(25*time.Hour)); !errors.Is(err, postgres.ErrPostNotEditable) {
		t.Fatalf("non-owner delete=%v", err)
	}
	if _, err := posts.Delete(context.Background(), owner, first.Post.ID, us1Now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	date, _ = repository.ListDate(context.Background(), owner, "2026-07-19", nil, nil, 51)
	if len(date) != 0 {
		t.Fatal("deleted post remains in memories")
	}
}

func TestUS4MemoryPaginationAndConcurrentInsert(t *testing.T) {
	pool, _ := us1Database(t)
	owner := uuid.New()
	addUser(t, pool, owner, "america")
	window := uuid.New()
	addWindow(t, pool, window, "america", false)
	for index := 0; index < 51; index++ {
		imageID, postID := uuid.New(), uuid.New()
		at := us1Now.Add(-time.Duration(index) * time.Second)
		if _, err := pool.Exec(context.Background(), `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at)VALUES($1,$2,$3,$4,10,'image/jpeg',$5)`, imageID, owner, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", imageID.String()+".jpg", at); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,posted_at,is_on_time,late_seconds)VALUES($1,$2,$3,$4,'memory','friends','none',$5,true,0)`, postID, owner, window, imageID, at); err != nil {
			t.Fatal(err)
		}
	}
	repository := postgres.NewMemoryRepository(&postgres.DB{Pool: pool})
	first, err := repository.ListMonth(context.Background(), owner, "2026-07", nil, nil, 51)
	if err != nil || len(first) != 51 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	cutoffAt, cutoffID := first[49].PostRecord.Post.PostedAt, first[49].PostRecord.Post.ID
	newImage, newPost := uuid.New(), uuid.New()
	_, _ = pool.Exec(context.Background(), `INSERT INTO image_objects(id,owner_user_id,sha256,object_key,byte_size,media_type,created_at)VALUES($1,$2,$3,$4,10,'image/jpeg',$5)`, newImage, owner, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", newImage.String()+".jpg", us1Now.Add(time.Hour))
	_, _ = pool.Exec(context.Background(), `INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,posted_at,is_on_time,late_seconds)VALUES($1,$2,$3,$4,'new','friends','none',$5,false,3598)`, newPost, owner, window, newImage, us1Now.Add(time.Hour))
	tail, err := repository.ListMonth(context.Background(), owner, "2026-07", &cutoffAt, &cutoffID, 51)
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail=%d err=%v", len(tail), err)
	}
	for _, record := range tail {
		if record.PostRecord.Post.ID == newPost {
			t.Fatal("concurrent insert crossed cursor")
		}
	}
}
