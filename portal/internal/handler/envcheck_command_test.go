package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvCheckDoesNotDisplayPreviousResult(t *testing.T) {
	for _, code := range []string{"1", "124"} {
		t.Run(code, func(t *testing.T) {
			dir := t.TempDir()
			result := filepath.Join(dir, "result.json")
			if err := os.WriteFile(result, []byte(`{"passed":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			// Model a service startup failure/timeout that never writes a result.
			script := "sudo() { if [ \"$1\" = systemctl ]; then return " + code + "; else command \"$@\"; fi; };\n" + strings.ReplaceAll(envCheckCommand, "/var/lib/isucon-envcheck/result.json", result)
			output, err := exec.Command("sh", "-c", script).CombinedOutput()
			if err == nil {
				t.Fatal("service failure was hidden")
			}
			if strings.Contains(string(output), `"passed":true`) {
				t.Fatalf("previous success was displayed: %s", output)
			}
		})
	}
}
