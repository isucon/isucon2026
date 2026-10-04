export function benchmarkUserData(image: string, region: string): string {
  if (
    !/^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com\/[a-zA-Z0-9/_-]+@sha256:[a-f0-9]{64}$/
      .test(image) || !/^[a-z]{2}-[a-z]+-[0-9]+$/.test(region)
  ) {
    throw new Error(
      "an ECR image pinned by digest and valid region are required",
    );
  }
  const registry = image.split("/")[0];
  return `#!/bin/bash
set -euo pipefail
dnf install -y docker
systemctl enable --now docker
aws ecr get-login-password --region ${region} | docker login --username AWS --password-stdin ${registry}
docker pull ${image}
install -d -m 0755 /var/log/isureal-bench
cat > /usr/local/bin/isureal-bench <<'CLI'
#!/bin/bash
set -euo pipefail
exec docker run --rm --network host --ulimit nofile=100000:100000 --entrypoint /usr/local/bin/isureal-bench ${image} "$@"
CLI
chmod 0755 /usr/local/bin/isureal-bench
`;
}
