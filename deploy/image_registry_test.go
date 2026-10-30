package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run with: go test deploy/image_registry_test.go
func TestExternalImagesUseExplicitRegistries(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = strings.TrimSpace(string(root))
	files, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Split(string(files), "\x00") {
		dockerfile := strings.HasSuffix(name, "Dockerfile")
		config := strings.Contains(name, "compose") || strings.HasPrefix(name, ".github/workflows/")
		if !dockerfile && !(config && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml"))) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cmd.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		stages := map[string]bool{"scratch": true, "photos-${PHOTOS}": true}
		for i, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			image := ""
			switch {
			case dockerfile && strings.HasPrefix(line, "# syntax="):
				image = strings.TrimPrefix(line, "# syntax=")
			case dockerfile && len(fields) >= 2 && fields[0] == "FROM":
				image = fields[1]
				if stages[image] {
					image = ""
				}
				if len(fields) == 4 && strings.EqualFold(fields[2], "AS") {
					stages[fields[3]] = true
				}
			case config && len(fields) >= 2 && fields[0] == "image:":
				image = strings.Trim(fields[1], "\"'")
				// These images are built locally by Compose.
				if image == "isureal-dev-app:local" || image == "isureal-dev-app-optimized:local" {
					image = ""
				}
			}
			if image == "" {
				continue
			}
			registry, _, qualified := strings.Cut(image, "/")
			if !qualified || !strings.Contains(registry, ".") || registry == "docker.io" || registry == "index.docker.io" {
				t.Errorf("%s:%d: image %q must use mirror.gcr.io for Docker Hub", name, i+1, image)
			}
		}
	}
}
