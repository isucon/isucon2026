package netcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

type recordingExecutor struct {
	outputs  [][]byte
	errors   []error
	commands []Command
}

func (e *recordingExecutor) Run(_ context.Context, command Command) ([]byte, error) {
	e.commands = append(e.commands, command)
	index := len(e.commands) - 1
	var output []byte
	var err error
	if index < len(e.outputs) {
		output = e.outputs[index]
	}
	if index < len(e.errors) {
		err = e.errors[index]
	}
	return output, err
}

func TestEnsureIPv4ForwardingAcceptsPreconfiguredNamespace(t *testing.T) {
	t.Parallel()
	executor := &recordingExecutor{outputs: [][]byte{[]byte("1\n")}}
	controller := NewController(config.Config{CommandTimeout: time.Second}, executor, nil)
	if err := controller.ensureIPv4Forwarding(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 1 || executor.commands[0].Name != "sysctl" || len(executor.commands[0].Args) != 2 || executor.commands[0].Args[0] != "-n" {
		t.Fatalf("unexpected commands: %+v", executor.commands)
	}
}

func TestEnsureIPv4ForwardingEnablesDisabledNamespace(t *testing.T) {
	t.Parallel()
	executor := &recordingExecutor{outputs: [][]byte{[]byte("0\n"), nil}}
	controller := NewController(config.Config{CommandTimeout: time.Second}, executor, nil)
	if err := controller.ensureIPv4Forwarding(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 2 || executor.commands[1].Args[0] != "-w" || executor.commands[1].Args[1] != "net.ipv4.ip_forward=1" {
		t.Fatalf("unexpected commands: %+v", executor.commands)
	}
}

func TestEnsureIPv4ForwardingReportsReadAndWriteFailures(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	writeErr := errors.New("write failed")
	executor := &recordingExecutor{errors: []error{readErr, writeErr}}
	controller := NewController(config.Config{CommandTimeout: time.Second}, executor, nil)
	err := controller.ensureIPv4Forwarding(context.Background())
	if !errors.Is(err, readErr) || !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want joined read/write failures", err)
	}
}

func TestApplyRawRecreatesEachDedicatedTCTree(t *testing.T) {
	t.Parallel()
	errorsByCall := make([]error, 16)
	errorsByCall[1] = errors.New("nft table absent")
	errorsByCall[4] = errors.New("root qdisc absent")
	errorsByCall[7] = errors.New("root qdisc absent")
	errorsByCall[10] = errors.New("root qdisc absent")
	errorsByCall[13] = errors.New("root qdisc absent")
	executor := &recordingExecutor{
		outputs: [][]byte{[]byte("1\n")},
		errors:  errorsByCall,
	}
	controller := NewController(config.Config{CommandTimeout: time.Second}, executor, nil)
	candidate := Candidate{
		Interfaces: map[domain.ServerID]string{
			domain.App1: "eth1",
			domain.App2: "eth2",
			domain.App3: "eth3",
			domain.DB1:  "eth4",
		},
		TCBatches: map[domain.ServerID]string{
			domain.App1: "qdisc replace dev eth1 root handle 1: htb\n",
			domain.App2: "qdisc replace dev eth2 root handle 1: htb\n",
			domain.App3: "qdisc replace dev eth3 root handle 1: htb\n",
			domain.DB1:  "qdisc replace dev eth4 root handle 1: htb\n",
		},
	}
	if err := controller.applyRaw(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int{{4, 5}, {7, 8}, {10, 11}, {13, 14}} {
		remove, apply := executor.commands[pair[0]], executor.commands[pair[1]]
		if remove.Name != "tc" || len(remove.Args) < 2 || remove.Args[0] != "qdisc" || remove.Args[1] != "del" {
			t.Fatalf("command %d does not remove root qdisc: %+v", pair[0], remove)
		}
		if apply.Name != "tc" || len(apply.Args) < 1 || apply.Args[0] != "-batch" {
			t.Fatalf("command %d does not apply tc batch: %+v", pair[1], apply)
		}
	}
}
