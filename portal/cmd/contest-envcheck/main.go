// contest-envcheck is the protected post-contest checker installed in both AMIs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isucon/isucon2026/portal/internal/contestcheck"
)

const stateDir = "/var/lib/isucon-envcheck"

type report struct {
	Token        string `json:"token,omitempty"`
	Name         string `json:"name"`
	Passed       bool   `json:"passed"`
	IP           string `json:"ip_address"`
	Message      string `json:"message"`
	AdminMessage string `json:"admin_message"`
	Raw          string `json:"raw_data"`
}

func main() {
	record := flag.Bool("record-baseline", false, "record the initial provisioning state once")
	baselinePath := flag.String("baseline", stateDir+"/baseline.json", "protected initial snapshot")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, *record, *baselinePath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, record bool, path string) error {
	r := report{Name: "post-contest", Message: "environment check failed"}
	current, err := contestcheck.CollectAWS(ctx)
	if err == nil {
		for _, i := range current.Instances {
			if i.ID == current.Self && i.Number == 5 {
				current.Router, err = contestcheck.CollectRouter(ctx, contestcheck.RunCommand)
				break
			}
		}
	}
	if err == nil && record {
		if failures := contestcheck.Topology(current); len(failures) > 0 {
			return fmt.Errorf("baseline topology: %s", strings.Join(failures, "; "))
		}
		if err = verifyInitial(current); err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		body, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return err
		}
		// A separate hard link publishes a complete file atomically and refuses to
		// replace an existing baseline, including a baseline from another instance.
		f, err := os.CreateTemp(filepath.Dir(path), ".baseline-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(body); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		return os.Link(f.Name(), path)
	}
	if err == nil {
		var baseline contestcheck.Snapshot
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			err = readErr
		} else {
			err = json.Unmarshal(body, &baseline)
		}
		if err == nil && (baseline.Version != 1 || len(contestcheck.Topology(baseline)) != 0) {
			err = fmt.Errorf("invalid provisioning baseline")
		}
		if err == nil {
			failures := contestcheck.Compare(baseline, current)
			r.Passed = len(failures) == 0
			r.Message = strings.Join(failures, "\n")
			if r.Passed {
				r.Message = "全ての追試環境チェックをパスしました"
			}
		}
	}
	if err != nil {
		r.Message = err.Error()
	}
	r.IP = current.PublicIP
	data, _ := json.Marshal(current)
	r.Raw = string(data)
	body, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(body))
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(stateDir+"/result.json", body, 0600); err != nil {
		return err
	}
	// CloudFormation supplies these credentials. Pulumi environments can be
	// inspected through the command result without installing portal credentials.
	credsBody, readErr := os.ReadFile("/etc/isucon/registration.json")
	if readErr == nil {
		var creds struct {
			Host  string `json:"host"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(credsBody, &creds); err != nil {
			return err
		}
		if creds.Host == "" || creds.Token == "" {
			return fmt.Errorf("incomplete portal credentials")
		}
		r.Token = creds.Token
		payload, _ := json.Marshal(r)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+creds.Host+"/api/env_checks", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("portal result request failed")
		}
		res.Body.Close()
		if res.StatusCode != 204 {
			return fmt.Errorf("portal result HTTP %d", res.StatusCode)
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	if !r.Passed {
		return fmt.Errorf("environment check failed")
	}
	return nil
}

// Provisioning writes the expected AMIs/types from its declarative configuration.
// The collected snapshot records resource IDs and additional immutable settings.
func verifyInitial(s contestcheck.Snapshot) error {
	body, err := os.ReadFile("/etc/isucon/envcheck.json")
	if err != nil {
		return err
	}
	return verifyExpected(s, body)
}
func verifyExpected(s contestcheck.Snapshot, body []byte) error {
	if failures := contestcheck.Topology(s); len(failures) > 0 {
		return fmt.Errorf("invalid initial topology: %s", strings.Join(failures, "; "))
	}
	var cfg struct {
		ServiceAMI string   `json:"service_ami"`
		RouterAMI  string   `json:"router_ami"`
		Types      []string `json:"types"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return err
	}
	if cfg.ServiceAMI == "" || cfg.RouterAMI == "" || len(cfg.Types) != 5 {
		return fmt.Errorf("incomplete expected environment configuration")
	}
	for _, i := range s.Instances {
		ami := cfg.ServiceAMI
		if i.Number == 5 {
			ami = cfg.RouterAMI
		}
		if i.AMI != ami || i.Type != cfg.Types[i.Number-1] {
			return fmt.Errorf("instance %d does not match provisioning AMI/type", i.Number)
		}
	}
	return nil
}
