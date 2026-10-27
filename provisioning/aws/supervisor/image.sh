#!/usr/bin/env bash
# ポータルの Supervisor 用のイメージを作って ECR へ push し、Pulumi の image に書く値（repository@digest）を表示する。
# supervisor/Dockerfile を PHOTOS=archive でビルドするので、var/photos/merged.zip（投稿に使う写真のアーカイブ）を
# /opt/isucon/photos/merged.zip に同梱する。アーカイブは make supervisor-photos で S3 から取る。
#
# 環境変数: SUPERVISOR_STACK（Supervisor stack、既定 portal-dev）、REPOSITORY（ECR の URL。省略時は stack の出力
# repositoryUrl）、TAG（省略時はイメージの内容から決める。同じタグが ECR にあれば push を省く）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
STACK_DIR="$ROOT/provisioning/aws/supervisor"
PHOTOS="$ROOT/var/photos/merged.zip"
SUPERVISOR_STACK="${SUPERVISOR_STACK:-portal-dev}"

log() { printf '[supervisor-image] %s\n' "$*" >&2; }
die() { log "$*"; exit 2; }

[[ -s "$PHOTOS" ]] || die "$PHOTOS がない。make supervisor-photos で取得する"

if [[ -z "${REPOSITORY:-}" ]]; then
	# Supervisor stack の backend と passphrase は provisioning/aws/supervisor/mise.toml と同じ値にする
	REPOSITORY="$(cd "$STACK_DIR" &&
		PULUMI_BACKEND_URL="${SUPERVISOR_PULUMI_BACKEND_URL:-s3://isucon2026-pulumi-state-124502390568/supervisor}" \
			PULUMI_CONFIG_PASSPHRASE="${PULUMI_CONFIG_PASSPHRASE-}" \
			pulumi stack output repositoryUrl --stack "$SUPERVISOR_STACK")"
fi
[[ "$REPOSITORY" == *.dkr.ecr.*.amazonaws.com/* ]] || die "ECR の repository ではない: $REPOSITORY"
REGISTRY="${REPOSITORY%%/*}"
REPOSITORY_NAME="${REPOSITORY#*/}"
REGION="$(cut -d. -f4 <<<"$REGISTRY")"

iid="$(mktemp)"
trap 'rm -f "$iid"' EXIT
log "building supervisor/Dockerfile with $PHOTOS"
# provenance を付けるとビルドのたびに ID が変わる
docker build --platform linux/amd64 --provenance=false --build-arg PHOTOS=archive \
	-f "$ROOT/supervisor/Dockerfile" -t isucon2026-supervisor:photos --iidfile "$iid" "$ROOT" >&2
id="$(cat "$iid")"
# 同梱したアーカイブが手元のものと一致することを確かめてから push する
expected="$(sha256sum "$PHOTOS" | cut -d' ' -f1)"
actual="$(docker run --rm --entrypoint /bin/sh "$id" -c 'sha256sum /opt/isucon/photos/merged.zip 2>/dev/null' | cut -d' ' -f1)"
[[ "$actual" == "$expected" ]] || die "the image does not contain $PHOTOS (sha256 ${actual:-none} != $expected)"
tag="${TAG:-photos-$(cut -d: -f2 <<<"$id" | cut -c1-16)}"

if digest="$(aws ecr describe-images --region "$REGION" --repository-name "$REPOSITORY_NAME" --image-ids "imageTag=$tag" \
	--query 'imageDetails[0].imageDigest' --output text 2>/dev/null)"; then
	log "image $tag is already in ECR"
else
	log "pushing $REPOSITORY:$tag"
	aws ecr get-login-password --region "$REGION" | docker login --username AWS --password-stdin "$REGISTRY" >/dev/null
	docker tag "$id" "$REPOSITORY:$tag"
	docker push "$REPOSITORY:$tag" >&2
	digest="$(aws ecr describe-images --region "$REGION" --repository-name "$REPOSITORY_NAME" --image-ids "imageTag=$tag" \
		--query 'imageDetails[0].imageDigest' --output text)"
fi
echo "$REPOSITORY@$digest"
