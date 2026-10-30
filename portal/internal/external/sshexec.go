package external

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHExecutor は選手インスタンスへの一括コマンド実行（FR-S-6）。
// isuadmin として接続し、ホスト鍵検証はしない（Rails の verify_host_key: :never を踏襲）。
type SSHExecutor struct {
	User           string
	PrivateKeyPath string
}

// Run はコマンドを実行して出力（stdout+stderr）と exit code を返す。
func (e *SSHExecutor) Run(host, command string) (string, int, error) {
	return e.RunVia("", host, command)
}
func validRouterAddress(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsPrivate()
}

// RunVia authenticates both SSH hops with the operator key. No agent/key is
// forwarded to the contestant server and no public router SSH ingress is needed.
func (e *SSHExecutor) RunVia(jump, host, command string) (string, int, error) {
	if jump != "" && !validRouterAddress(host) {
		return "", -1, fmt.Errorf("router data ENI address is missing or invalid")
	}
	key, err := os.ReadFile(e.PrivateKeyPath)
	if err != nil {
		return "", -1, fmt.Errorf("ssh: read key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", -1, fmt.Errorf("ssh: parse key: %w", err)
	}
	user := e.User
	if user == "" {
		user = "isuadmin"
	}

	cfg := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second}
	var client *ssh.Client
	if jump == "" {
		client, err = ssh.Dial("tcp", net.JoinHostPort(host, "22"), cfg)
	} else {
		bastion, dialErr := ssh.Dial("tcp", net.JoinHostPort(jump, "22"), cfg)
		if dialErr != nil {
			return "", -1, dialErr
		}
		defer bastion.Close()
		jumpTimeout := time.AfterFunc(4*time.Minute, func() { bastion.Close() })
		defer jumpTimeout.Stop()
		conn, dialErr := bastion.Dial("tcp", net.JoinHostPort(host, "22"))
		if dialErr != nil {
			return "", -1, dialErr
		}
		handshake, chans, requests, dialErr := ssh.NewClientConn(conn, net.JoinHostPort(host, "22"), cfg)
		if dialErr != nil {
			conn.Close()
			return "", -1, dialErr
		}
		client = ssh.NewClient(handshake, chans, requests)
	}
	if err != nil {
		return "", -1, err
	}
	defer client.Close()
	timer := time.AfterFunc(4*time.Minute, func() { client.Close() })
	defer timer.Stop()

	sess, err := client.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf
	if err := sess.Run(command); err != nil {
		var exitErr *ssh.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return buf.String(), exitErr.ExitStatus(), nil
		}
		return buf.String(), -1, err
	}
	return buf.String(), 0, nil
}

func asExitError(err error, target **ssh.ExitError) bool {
	e, ok := err.(*ssh.ExitError)
	if ok {
		*target = e
	}
	return ok
}
