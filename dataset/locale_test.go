package dataset

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRegionalLanguage(t *testing.T) {
	w := Generate(Large())
	seen := map[string]map[string]bool{}
	usernames := map[string]bool{}
	displayNames := map[string]bool{}
	kinds := map[string]map[bool]int{}
	pattern := regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	for index, u := range w.Users {
		if !pattern.MatchString(u.Username) || usernames[u.Username] {
			t.Fatalf("invalid or duplicate username %q", u.Username)
		}
		usernames[u.Username] = true

		l := languageFor(u.Region, u.ID)
		if seen[u.Region] == nil {
			seen[u.Region] = map[string]bool{}
		}
		seen[u.Region][l.code] = true
		baseName := strings.TrimSuffix(u.DisplayName, " "+strconv.Itoa(index))
		if displayNames[u.DisplayName] {
			t.Fatalf("duplicate display name %q", u.DisplayName)
		}
		displayNames[u.DisplayName] = true
		personal := slices.Contains(l.personalNames, baseName)
		key := u.Region + "/" + l.code
		if kinds[key] == nil {
			kinds[key] = map[bool]int{}
		}
		kinds[key][personal]++
		if !personal && !slices.Contains(l.names, baseName) {
			t.Fatalf("%s: unexpected name %q", l.code, u.DisplayName)
		}
		if u.Bio != "" && !slices.Contains(l.bios, u.Bio) {
			t.Fatalf("%s: unexpected bio %q", l.code, u.Bio)
		}
		names, handles := l.names, l.handles
		if personal {
			names, handles = l.personalNames, l.personalHandles
		}
		team := slices.Index(names, baseName)
		valid := u.Username == handles[team]+"_"+strconv.Itoa(index)
		if !valid {
			t.Fatalf("%s: unexpected username %q", l.code, u.Username)
		}
	}
	for _, region := range Regions {
		if len(seen[region]) != 3 {
			t.Fatalf("%s languages: %v", region, seen[region])
		}
	}
	for key, set := range kinds {
		if len(set) != 2 || set[true] < set[false] || set[true] > set[false]+1 {
			t.Fatalf("%s must contain ordinary and invented names", key)
		}
	}
	crossLanguage := 0
	for day := range 3 {
		for _, p := range w.PostsOn(day) {
			author := w.Users[w.userIndex[p.AuthorID]]
			l := languageFor(author.Region, author.ID)
			if !slices.Contains(l.captions, p.Caption) {
				t.Fatalf("%s: unexpected caption %q", l.code, p.Caption)
			}
			if p.Location.Locality != nil && !slices.Contains(l.localities, *p.Location.Locality) {
				t.Fatalf("%s: unexpected locality", l.code)
			}
			for _, c := range p.Comments {
				commenter := w.Users[w.userIndex[c.AuthorID]]
				cl := languageFor(commenter.Region, commenter.ID)
				if cl.code != l.code {
					crossLanguage++
				}
				if !slices.Contains(cl.comments, c.Body) {
					t.Fatalf("comment uses wrong author's language: %s %q", cl.code, c.Body)
				}
			}
		}
	}
	if crossLanguage == 0 {
		t.Fatal("no cross-language comments exercised")
	}
}

func TestLanguageCatalog(t *testing.T) {
	for _, region := range Regions {
		if len(regionLanguages[region]) != 3 {
			t.Fatalf("%s needs three languages", region)
		}
		for _, l := range regionLanguages[region] {
			if len(l.names) != 20 || len(l.handles) != len(l.names) {
				t.Fatalf("%s missing translated team names", l.code)
			}
			if len(l.personalNames) != 10 || len(l.personalHandles) != len(l.personalNames) {
				t.Fatalf("%s missing ordinary names", l.code)
			}
			names, handles := map[string]bool{}, map[string]bool{}
			allNames := append(append([]string{}, l.names...), l.personalNames...)
			allHandles := append(append([]string{}, l.handles...), l.personalHandles...)
			pattern := regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
			for i, name := range allNames {
				if utf8.RuneCountInString(name) > 100 || names[name] {
					t.Fatalf("%s invalid or duplicate display name %q", l.code, name)
				}
				names[name] = true
				handle := allHandles[i]
				if !pattern.MatchString(handle+"_4999") || handles[handle] {
					t.Fatalf("%s invalid or duplicate handle %q", l.code, handle)
				}
				handles[handle] = true
			}
			for _, values := range [][]string{l.names, l.handles, l.personalNames, l.personalHandles, l.bios, l.comments, l.localities} {
				if len(values) == 0 {
					t.Fatalf("%s empty catalog", l.code)
				}
				for _, value := range values {
					if value == "" {
						t.Fatalf("%s empty value", l.code)
					}
				}
			}
			if len(l.captions) != 16 || len(l.comments) != 14 || len(l.localities) != 8 {
				t.Fatalf("%s changes historical random draws", l.code)
			}
			if !slices.Contains(l.captions, "") {
				t.Fatalf("%s missing empty caption", l.code)
			}
		}
	}
}
