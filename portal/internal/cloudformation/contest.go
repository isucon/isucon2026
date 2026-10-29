package cloudformation

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Each participant stack has its own VPC, so private addresses do not depend on
// the portal team ID. Numbers 1–3 are apps, 4 is the database, and 5 is the Router.
type contestServer struct {
	Number                   int
	ID, Region, InstanceType string
}

func (params) ContestServers() []contestServer {
	return []contestServer{{1, "App1", "asia", "c7i.large"}, {2, "App2", "europe", "c7i.large"}, {3, "App3", "america", "c7i.large"}, {4, "Database", "", "c6in.large"}}
}
func (p params) Registration(number int) string {
	b, _ := json.Marshal(map[string]any{"host": p.PortalHost, "token": p.Token, "number": number})
	return base64.StdEncoding.EncodeToString(b)
}
func (params) RegistrationScript() string { return compressedFile("files/register.py") }
func (params) RouterBootstrap() string    { return compressedFile("files/router-bootstrap.py") }

// cloud-init write_files decodes gz+b64, keeping the downloadable template below
// CloudFormation's 51,200-byte upload limit without modifying bootstrap scripts.
func compressedFile(name string) string {
	data, err := templatesFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
func (p params) AppHost() string { return fmt.Sprintf("app.team%d-aws.127.0.0.1.nip.io", p.TeamID) }
