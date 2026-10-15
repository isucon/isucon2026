#!/bin/sh
set -eu

sudo install -m 0755 /tmp/isucon-env-checker /usr/local/bin/isucon-env-checker
sudo useradd --create-home --shell /usr/local/bin/isucon-env-checker isucon
sudo install -d -m 0700 -o isucon -g isucon /home/isucon/.ssh
sudo install -d -m 0755 /opt/isucon-env-checker
sudo install -m 0644 /tmp/isucon-env-checker.service /etc/systemd/system/isucon-env-checker.service
sudo tee /etc/ssh/sshd_config.d/isucon-envcheck.conf >/dev/null <<'EOF'
Match User isucon
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no
EOF
sudo sshd -t
sudo systemctl enable isucon-env-checker.service
sudo cloud-init clean --logs
sudo truncate -s 0 /etc/machine-id /home/ubuntu/.ssh/authorized_keys
rm /tmp/isucon-env-checker /tmp/isucon-env-checker.service
