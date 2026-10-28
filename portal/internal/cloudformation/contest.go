package cloudformation

import (
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
func (params) RegistrationScript() string {
	b, err := templatesFS.ReadFile("files/register.py")
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
func (params) RouterBootstrap() string {
	b, err := templatesFS.ReadFile("files/router-bootstrap.py")
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
func (p params) AppHost() string { return fmt.Sprintf("app.team%d-aws.127.0.0.1.nip.io", p.TeamID) }
