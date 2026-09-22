package domain

import (
	"testing"
	"time"
)

func TestInitialStates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	host := HostnameConfig{RandomHash: "hash", DomainSuffix: "example.test"}
	standIn, err := InitialState(TeamKey{Number: 1}, host, RouterModeStandIn, StandInProfileSuccess, now, now, "snapshot-1")
	if err != nil {
		t.Fatal(err)
	}
	if !standIn.Converged || standIn.Applied == nil || standIn.Apply.State != ApplyConverged {
		t.Fatalf("invalid stand-in state: %+v", standIn)
	}
	external, err := InitialState(TeamKey{Number: 1}, host, RouterModeExternalRouter, StandInProfileNone, now, now, "snapshot-2")
	if err != nil {
		t.Fatal(err)
	}
	if external.Converged || external.Applied != nil || external.Apply.State != ApplyUnsynced {
		t.Fatalf("invalid external state: %+v", external)
	}
	if standIn.Desired.Calculated.AppHostname != external.Desired.Calculated.AppHostname {
		t.Fatal("hostname must be stable across initialization")
	}
}
