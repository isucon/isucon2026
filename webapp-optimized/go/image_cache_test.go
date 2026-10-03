package isureal

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestImageCacheBoundsAndEviction(t *testing.T) {
	c := newImageCache(6, 2)
	c.put("a", imageObject{Version: "1", Data: []byte("aaa")}, time.Time{})
	c.put("b", imageObject{Version: "1", Data: []byte("bbb")}, time.Time{})
	held, ok := c.get("a")
	if !ok {
		t.Fatal("cache miss")
	}
	c.put("c", imageObject{Version: "1", Data: []byte("ccc")}, time.Time{})
	if _, ok := c.get("b"); ok {
		t.Fatal("least recently used entry retained")
	}
	c.put("a", imageObject{Version: "2", Data: []byte("new")}, time.Time{})
	if string(held.data) != "aaa" {
		t.Fatal("in-flight reference changed")
	}
	c.put("large", imageObject{Version: "1", Data: []byte("too large")}, time.Time{})
	if _, ok := c.get("large"); ok {
		t.Fatal("oversized image retained")
	}
	for i := range 20 {
		c.put(fmt.Sprint(i), imageObject{Version: "1", Data: []byte{}}, time.Time{})
	}
	if c.used > 6 || len(c.entries) > 2 || c.recent.Len() > 2 {
		t.Fatal("cache exceeds limits")
	}
}

func TestImageCacheConcurrentReaders(t *testing.T) {
	c := newImageCache(64, 4)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			id := fmt.Sprint(i % 8)
			for range 20 {
				c.put(id, imageObject{Version: "version", Data: []byte(id)}, time.Time{})
				if got, ok := c.get(id); ok && string(got.data) != id {
					t.Errorf("wrong image: %s", got.data)
				}
			}
		})
	}
	wg.Wait()
	if c.used > 64 || len(c.entries) > 4 {
		t.Fatal("cache exceeds limits")
	}
}
