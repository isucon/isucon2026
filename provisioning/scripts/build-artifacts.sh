#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_file="${VM_RELEASE:-${repo_root}/provisioning/config/release.json}"

if [[ -f "${release_file}" ]]; then
  release_id="$(mise exec -- python -c 'import json,sys; print(json.load(open(sys.argv[1]))["releaseId"])' "${release_file}")"
else
  release_id="development"
fi

artifact_root="${repo_root}/provisioning/.state/artifacts/${release_id}"
bin_dir="${artifact_root}/bin"
frontend_dir="${artifact_root}/frontend"
share_dir="${artifact_root}/share"
bundle_dir="${artifact_root}/bundles"
mkdir -p "${bin_dir}" "${frontend_dir}" "${share_dir}/migrations" "${bundle_dir}"

for command in api worker image-sanitizer migrate reset seed; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go -C "${repo_root}/backend" build -buildvcs=false -trimpath -ldflags='-buildid=' \
    -o "${bin_dir}/${command}" "./cmd/${command}"
done

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go -C "${repo_root}/router" build -buildvcs=false -trimpath -ldflags='-buildid=' \
  -o "${bin_dir}/isureal-router" ./cmd/router

npm --prefix "${repo_root}/frontend" ci
npm --prefix "${repo_root}/frontend" run build
rsync -a --delete "${repo_root}/frontend/dist/" "${frontend_dir}/"
rsync -a --delete "${repo_root}/backend/migrations/" "${share_dir}/migrations/"
install -m 0644 "${repo_root}/specs/001-isureal-social-app/contracts/openapi.yaml" "${share_dir}/openapi.yaml"

envoy_image="${ENVOY_IMAGE:-envoyproxy/envoy:v1.32.2}"
docker pull "${envoy_image}" >/dev/null
envoy_container="$(docker create "${envoy_image}")"
cleanup() {
  docker rm -f "${envoy_container}" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker cp "${envoy_container}:/usr/local/bin/envoy" "${bin_dir}/envoy"
chmod 0755 "${bin_dir}/envoy"
cleanup
trap - EXIT

tar_flags=(--sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner)
tar "${tar_flags[@]}" -cf "${bundle_dir}/backend.tar" -C "${artifact_root}" \
  bin/api bin/worker bin/image-sanitizer bin/migrate bin/reset bin/seed share/openapi.yaml
tar "${tar_flags[@]}" -cf "${bundle_dir}/frontend.tar" -C "${artifact_root}" frontend
tar "${tar_flags[@]}" -cf "${bundle_dir}/router.tar" -C "${artifact_root}" bin/isureal-router
tar "${tar_flags[@]}" -cf "${bundle_dir}/envoy.tar" -C "${artifact_root}" bin/envoy
tar "${tar_flags[@]}" -cf "${bundle_dir}/database-migrations.tar" -C "${artifact_root}" share/migrations

rm -f "${artifact_root}/manifest.json" "${artifact_root}/release.json"

ln -sfn "${release_id}" "${repo_root}/provisioning/.state/artifacts/current"
printf 'artifacts: %s\n' "${artifact_root}"
