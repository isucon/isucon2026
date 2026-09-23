package external

import (
	"bytes"
	"fmt"
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
	client, err := ssh.Dial("tcp", host+":22", &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return "", -1, err
	}
	defer client.Close()

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
