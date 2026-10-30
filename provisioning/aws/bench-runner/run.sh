#!/usr/bin/env bash
# ベンチを Fargate で 1 回走らせる。ビルド → ECR へ push → ecspresso run → S3 から結果を取得 → 要約を表示。
#
#   run.sh run           TEAM（必須。8,9,10 のように並べると各チームへ同時に流す）、BENCH_ARGS、
#                        IMAGE（指定するとビルドと push を省く）、HOST_STATS=0（チームのサーバーの統計を取らない）、
#                        PHOTOS（投稿に使う写真のアーカイブの S3 キー。既定 assets/merged.zip。none なら生成画像）、
#                        DIGESTS（初期データの画像のダイジェストの表の S3 キー。既定 assets/image-digests-large.bin。
#                        none なら渡さず、ベンチは画像を描き直して比べる）
#   run.sh image         ビルドと push だけ行い、イメージ（repository@digest）を表示する
#   run.sh show [RUN_ID] 取得済みの結果の要約を表示する（省略すると最新）
#
# 環境変数: STACK（チーム環境の stack、既定 dev）、RUNNER_STACK（この基盤の stack、既定 dev）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
RUNNER_DIR="$ROOT/provisioning/aws/bench-runner"
RESULTS_DIR="$ROOT/var/aws-bench"
ECSPRESSO_VERSION=2.8.7
STACK="${STACK:-dev}"
RUNNER_STACK="${RUNNER_STACK:-dev}"
export PULUMI_BACKEND_URL="${PULUMI_BACKEND_URL:-s3://isucon2026-pulumi-state}"

log() { printf '[aws-bench] %s\n' "$*" >&2; }
die() { log "$*"; exit 2; }

ecspresso() {
	if command -v mise >/dev/null; then
		mise exec "aqua:kayac/ecspresso@${ECSPRESSO_VERSION}" -- ecspresso "$@"
	else
		command ecspresso "$@"
	fi
}

# 複数チームへ同時に流すときは、親が読んだ Pulumi の出力を BENCH_RUN_INFRA と BENCH_RUN_TEAM_OUTPUTS で子に渡す
load_infra() {
	INFRA="${BENCH_RUN_INFRA:-$(pulumi stack output infra --json --stack "$RUNNER_STACK" -C "$RUNNER_DIR")}"
	REPOSITORY="$(jq -r .repository <<<"$INFRA")"
	REPOSITORY_NAME="$(jq -r .repositoryName <<<"$INFRA")"
	BUCKET="$(jq -r .bucket <<<"$INFRA")"
	CLUSTER="$(jq -r .cluster <<<"$INFRA")"
	FAMILY="$(jq -r .name <<<"$INFRA")"
	export AWS_REGION="$(jq -r .region <<<"$INFRA")"
}

# supervisor/Dockerfile をそのまま使い、本番の Supervisor と同じ bench のバイナリを作る。
# タグはイメージの内容から決めるので、変更がなければ push を省く。
build_and_push() {
	local iid id tag digest
	iid="$(mktemp)"
	log "building supervisor/Dockerfile"
	# provenance を付けるとビルドのたびに ID が変わる
	docker build --platform linux/amd64 --provenance=false -f "$ROOT/supervisor/Dockerfile" \
		-t isucon2026-bench-runner:local --iidfile "$iid" "$ROOT" >&2
	id="$(cat "$iid")"
	rm -f "$iid"
	tag="$(cut -d: -f2 <<<"$id" | cut -c1-16)"
	if digest="$(aws ecr describe-images --repository-name "$REPOSITORY_NAME" --image-ids "imageTag=$tag" \
		--query 'imageDetails[0].imageDigest' --output text 2>/dev/null)"; then
		log "image $tag is already in ECR"
	else
		log "pushing $REPOSITORY:$tag"
		aws ecr get-login-password | docker login --username AWS --password-stdin "${REPOSITORY%%/*}" >/dev/null
		docker tag "$id" "$REPOSITORY:$tag"
		docker push "$REPOSITORY:$tag" >&2
		digest="$(aws ecr describe-images --repository-name "$REPOSITORY_NAME" --image-ids "imageTag=$tag" \
			--query 'imageDetails[0].imageDigest' --output text)"
	fi
	IMAGE="$REPOSITORY@$digest"
}

# 同じ Host へ走行中のタスクがあれば止める（initialize が互いのデータを消すため）
ensure_idle() {
	local host="$1" tasks busy
	tasks="$(aws ecs list-tasks --cluster "$CLUSTER" --family "$FAMILY" --query 'taskArns' --output json)"
	[[ "$(jq length <<<"$tasks")" == 0 ]] && return
	busy="$(aws ecs describe-tasks --cluster "$CLUSTER" --tasks $(jq -r '.[]' <<<"$tasks") --output json |
		jq -r --arg host "$host" '.tasks[] | select(any(.overrides.containerOverrides[]?.command[]?; . == $host)) | .taskArn')"
	[[ -z "$busy" ]] || die "another benchmark is running against $host: $busy"
}

# チームのサーバーでも sampler.sh を動かす。SSM の出力（統計の tar.gz を base64 にしたもの）は
# バケットの ssm/<RUN_ID>/ に置かれる
HOST_STATS_DIR=/tmp/isureal-bench-stats
# Router の HAProxy の統計（runtime API の show stat）を「プロキシ.サーバー.列: 値」の行にする。ほかのホストでは何も出さない。
# 18001 で応答を閉じない相手につないでも SSM のタイムアウトまで止まらないよう、5 秒で打ち切る
HAPROXY_STATS="$(
	cat <<'EOF'
timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/18001 && echo "show stat" >&3 && cat <&3' 2>/dev/null | awk -F, 'NR == 1 { sub(/^# /, ""); for (i = 1; i <= NF; i++) column[i] = $i; next } NF > 2 { for (i = 3; i <= NF; i++) if ($i ~ /^[0-9]+$/) print $1 "." $2 "." column[i] ": " $i }'
EOF
)"

ssm_send() {
	local run_id="$1" comment="$2" script="$3"
	shift 3
	aws ssm send-command --document-name AWS-RunShellScript --comment "$comment" --instance-ids "$@" \
		--timeout-seconds 120 --output-s3-bucket-name "$BUCKET" --output-s3-key-prefix "ssm/$run_id" \
		--parameters "$(jq -n --arg script "$script" '{commands: [$script]}')" \
		--query Command.CommandId --output text
}

ssm_wait() {
	local cid="$1" id
	shift
	for id in "$@"; do
		aws ssm wait command-executed --command-id "$cid" --instance-id "$id" 2>/dev/null ||
			log "SSM command $cid failed on $id"
	done
}

# Router、app、db のインスタンス ID と役割（Name タグの最後の部分）を "ID 役割" の行で返す
team_hosts() {
	local ids
	ids="$(jq -r '[.routerInstanceId // empty] + (.serviceInstanceIds // []) | .[]' <<<"$1")"
	[[ -n "$ids" ]] || return 0
	# shellcheck disable=SC2086
	aws ec2 describe-instances --instance-ids $ids \
		--query 'Reservations[].Instances[].[InstanceId, Tags[?Key==`Name`]|[0].Value]' --output text |
		awk '{ n = split($2, part, "-"); print $1, part[n] }'
}

start_host_stats() {
	local run_id="$1"
	shift
	local script
	script="$(
		cat <<EOF
set -eu
d=$HOST_STATS_DIR/$run_id
rm -rf "\$d"
mkdir -p "\$d"
cat >"\$d/sampler.sh" <<'SAMPLER'
$(cat "$RUNNER_DIR/sampler.sh")
SAMPLER
tc -s qdisc show >"\$d/tc-start.txt" 2>&1 || true
$HAPROXY_STATS >"\$d/haproxy-start.txt"
[ -s "\$d/haproxy-start.txt" ] || rm -f "\$d/haproxy-start.txt"
nohup setsid sh "\$d/sampler.sh" "\$d/stop" 1800 >"\$d/stats.tsv" 2>/dev/null </dev/null &
EOF
	)"
	ssm_wait "$(ssm_send "$run_id" "isureal bench stats start $run_id" "$script" "$@")" "$@"
}

collect_host_stats() {
	local run_id="$1" dir="$2" hosts="$3"
	local script cid tmp id role out
	script="$(
		cat <<EOF
d=$HOST_STATS_DIR/$run_id
[ -d "\$d" ] || exit 1
touch "\$d/stop"
sleep 2
tc -s qdisc show >"\$d/tc-end.txt" 2>&1 || true
[ ! -e "\$d/haproxy-start.txt" ] || $HAPROXY_STATS >"\$d/haproxy-end.txt"
rm -f "\$d/sampler.sh" "\$d/stop"
tar -C "\$d" -czf - . | base64 -w0
rm -rf "\$d"
EOF
	)"
	# shellcheck disable=SC2046
	cid="$(ssm_send "$run_id" "isureal bench stats collect $run_id" "$script" $(cut -d' ' -f1 <<<"$hosts"))"
	# shellcheck disable=SC2046
	ssm_wait "$cid" $(cut -d' ' -f1 <<<"$hosts")
	tmp="$(mktemp -d)"
	aws s3 cp --recursive --only-show-errors "s3://$BUCKET/ssm/$run_id/$cid/" "$tmp/" || true
	while read -r id role; do
		out="$tmp/$id/awsrunShellScript/0.awsrunShellScript/stdout"
		if [[ -s "$out" ]]; then
			mkdir -p "$dir/hosts/$role"
			base64 -d <"$out" | tar -xz -C "$dir/hosts/$role" || log "failed to extract stats of $role"
		else
			log "no stats from $role ($id)"
		fi
	done <<<"$hosts"
	rm -rf "$tmp"
}

# ディスクの実測値と比べるため、サーバーの EBS ボリュームとインスタンスタイプの上限を hosts/<役割>/ebs.json に書く
save_ebs_limits() {
	local dir="$1" hosts="$2" instances volumes types id role
	# shellcheck disable=SC2046
	instances="$(aws ec2 describe-instances --instance-ids $(cut -d' ' -f1 <<<"$hosts") --output json \
		--query 'Reservations[].Instances[].{id: InstanceId, type: InstanceType, volumes: BlockDeviceMappings[].Ebs.VolumeId}')"
	# shellcheck disable=SC2046
	volumes="$(aws ec2 describe-volumes --volume-ids $(jq -r '.[].volumes[]' <<<"$instances") --output json \
		--query 'Volumes[].{id: VolumeId, type: VolumeType, size: Size, iops: Iops, throughput: Throughput}')"
	# shellcheck disable=SC2046
	types="$(aws ec2 describe-instance-types --instance-types $(jq -r '[.[].type] | unique | .[]' <<<"$instances") \
		--output json --query 'InstanceTypes[].{type: InstanceType, ebs: EbsInfo.EbsOptimizedInfo}')"
	while read -r id role; do
		[[ -d "$dir/hosts/$role" ]] || continue
		jq -n --arg id "$id" --argjson instances "$instances" --argjson volumes "$volumes" --argjson types "$types" '
			($instances[] | select(.id == $id)) as $instance
			| {instanceType: $instance.type,
				instanceLimits: ($types[] | select(.type == $instance.type) | .ebs),
				volumes: [$volumes[] | select(.id as $volume | $instance.volumes | index($volume))]}' \
			>"$dir/hosts/$role/ebs.json"
	done <<<"$hosts"
}

show() {
	local dir="$1" result="$1/result.json"
	jq -r '"run:     \(.runId)", "team:    \(.team) (\(.host) via \(.target))", "commit:  \(.commit)", "image:   \(.image)", "args:    \(.benchArgs | join(" "))", "exit:    \(.exitCode)"' "$dir/meta.json"
	if [[ ! -s "$result" ]]; then
		log "result.json is missing; see $dir/bench.log or CloudWatch Logs"
		return
	fi
	jq -r '
		"pass:    \(.pass)", "score:   \(.score)",
		(if .reason then "reason:  \(.reason)" else empty end),
		"seed:    \(.seed)", "users:   \(.users | tojson)", "grandPrix: \(.grandPrix | tojson)", "reputation: \(.reputation)",
		"breakdown: \(.breakdown | tojson)",
		"days (region day: users at the start + newcomers -> gate passes (gate p50/p90), failure rate; from the previous day: quick passes, missed):",
		((.days // [])[] | "  \(.region) \(.day): \(.population) + \(.newcomers) -> \(.passes) (\((.gateP50 // 0) / 1e6 | round)/\((.gateP90 // 0) / 1e6 | round) ms), \(.failRate * 10000 | round / 100)% of \(.ops); quick \(.quick // 0), missed \(.missed // 0)"),
		(.critical[]? | "critical: \(.)"),
		((.messages // [])[:20][] | "message: \(.)")
	' "$result"
	if [[ -e "$dir/stats.tsv" || -d "$dir/hosts" ]]; then
		echo
		python3 "$RUNNER_DIR/report.py" "$dir" | tee "$dir/report.txt"
	fi
	log "files: $dir"
}

team_outputs() {
	if [[ -n "${BENCH_RUN_TEAM_OUTPUTS:-}" ]]; then
		printf '%s' "$BENCH_RUN_TEAM_OUTPUTS"
	else
		pulumi stack output teamOutputs --json --stack "$STACK" -C "$ROOT/provisioning/aws"
	fi
}

# 複数チームへ同時に流す。ビルド、push、Pulumi の読み出し、タスク定義の登録は 1 回だけ行い、チームごとの run を
# 並行して起動する。タスク定義はチームによらず同じで、同じ family へ同時に登録すると ECS が拒否するので、
# 子の run は親が登録したリビジョンを使う。各チームの出力には [teamN] を付け、最後に全チームの結果をまとめて表示する。
run_many() {
	local t
	for t in "$@"; do
		[[ "$t" =~ ^[1-9][0-9]*$ ]] || die "invalid team number: $t"
	done
	[[ "$(printf '%s\n' "$@" | sort -u | wc -l)" -eq $# ]] || die "duplicate teams: $*"
	load_infra
	[[ -n "${IMAGE:-}" ]] || build_and_push
	mkdir -p "$RESULTS_DIR"
	[[ -e "$RESULTS_DIR/runs.tsv" ]] || printf 'run\tteam\tcommit\tpass\tscore\targs\n' >"$RESULTS_DIR/runs.tsv"
	local parent pids=() status=0 pid
	parent="$(mktemp -d)"
	BENCH_RUN_TEAM_OUTPUTS="$(team_outputs)"
	BENCH_RUN_REVISION="$(cd "$RUNNER_DIR/ecspresso" && BENCH_RUNNER_INFRA="$INFRA" BENCH_IMAGE="$IMAGE" \
		ecspresso register --no-color --config ecspresso.jsonnet --output 2>"$parent/register.log" | jq -r .revision)"
	if [[ ! "$BENCH_RUN_REVISION" =~ ^[0-9]+$ ]]; then
		awk '/^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { skip = /\[DEBUG\]/ } !skip' "$parent/register.log" >&2
		die "failed to register the task definition"
	fi
	export IMAGE BENCH_RUN_INFRA="$INFRA" BENCH_RUN_TEAM_OUTPUTS BENCH_RUN_PARENT="$parent" BENCH_RUN_REVISION
	log "running teams $* in parallel with $IMAGE (task definition revision $BENCH_RUN_REVISION)"
	for t in "$@"; do
		(TEAM="$t" "$0" run 2>&1 | sed -u "s/^/[team$t] /") &
		pids+=("$!")
	done
	for pid in "${pids[@]}"; do
		wait "$pid" || status=1
	done

	echo
	printf '%-5s %-24s %-5s %9s %4s\n' team run pass score exit
	local run_id dir
	for t in "$@"; do
		run_id="$(cat "$parent/$t" 2>/dev/null || true)"
		dir="$RESULTS_DIR/$run_id"
		if [[ -z "$run_id" || ! -s "$dir/result.json" ]]; then
			printf '%-5s %s\n' "$t" "${run_id:-(not started)} no result; see the [team$t] log above"
			status=1
			continue
		fi
		# shellcheck disable=SC2046
		printf '%-5s %-24s %-5s %9s %4s\n' "$t" "$run_id" $(jq -r '[.pass, .score] | @tsv' "$dir/result.json") \
			"$(jq -r .exitCode "$dir/meta.json")"
		[[ "$(jq -r .exitCode "$dir/meta.json")" == 0 ]] || status=1
	done
	for t in "$@"; do
		run_id="$(cat "$parent/$t" 2>/dev/null || true)"
		[[ -n "$run_id" && -e "$RESULTS_DIR/$run_id/report.txt" ]] || continue
		grep '^! ' "$RESULTS_DIR/$run_id/report.txt" | sed "s/^! /  team$t: /" || true
	done
	rm -r -- "$parent"
	log "details: make aws-bench-show RUN=<run>"
	return "$status"
}

cmd_run() {
	local teams list="${TEAM:-}"
	read -r -a teams <<<"${list//,/ }"
	if ((${#teams[@]} > 1)); then
		run_many "${teams[@]}"
		return
	fi
	[[ "${TEAM:-}" =~ ^[1-9][0-9]*$ ]] || die "set TEAM to the team number in the $STACK stack"
	load_infra
	local team target host
	team="$(team_outputs | jq -c --argjson n "$TEAM" '.[] | select(.number == $n)')"
	target="$(jq -r '.routerPublicAddress // empty' <<<"$team")"
	host="$(jq -r '.appHostname // empty' <<<"$team")"
	[[ -n "$target" && -n "$host" ]] || die "team $TEAM has no Router in the $STACK stack"
	ensure_idle "$host"

	[[ -n "${IMAGE:-}" ]] || build_and_push
	local run_id dir extra args record_args overrides rc started commit
	commit="$(git -C "$ROOT" rev-parse --short HEAD)"
	# イメージに入るディレクトリに未コミットの変更があれば印を付ける
	[[ -z "$(git -C "$ROOT" status --porcelain -- bench dataset supervisor portal)" ]] || commit+="+dirty"
	run_id="$(date -u +%Y%m%dT%H%M%SZ)-team$TEAM"
	dir="$RESULTS_DIR/$run_id"
	mkdir -p "$dir"
	[[ -z "${BENCH_RUN_PARENT:-}" ]] || echo "$run_id" >"$BENCH_RUN_PARENT/$TEAM"
	read -r -a extra <<<"${BENCH_ARGS:-}"
	# 写真のアーカイブは実行のたびに署名付き URL にする。一時的な認証情報で署名した URL は、
	# 認証情報の期限で失効するため。記録には URL ではなく S3 の場所を残す。
	local photos="${PHOTOS:-assets/merged.zip}" photo_args=() photo_record=()
	if [[ "$photos" != none && " ${extra[*]} " != *" -images "* ]]; then
		photo_args=(-images "$(aws s3 presign "s3://$BUCKET/$photos" --expires-in 3600)")
		photo_record=(-images "s3://$BUCKET/$photos")
	fi
	# 初期データの画像のダイジェストの表も署名付き URL で渡す。引数ではなく環境変数にするのは、
	# IMAGE で指定した古いベンチ（表を読まない）でもそのまま走らせるため。
	local digests="${DIGESTS:-assets/image-digests-large.bin}" digest_url="" digest_record=""
	if [[ "$digests" != none ]]; then
		digest_url="$(aws s3 presign "s3://$BUCKET/$digests" --expires-in 3600)"
		digest_record="s3://$BUCKET/$digests"
	fi
	args="$(jq -n '$ARGS.positional' --args -- \
		-target "$target" -target-port 8080 -host "$host" -scale large "${photo_args[@]}" "${extra[@]}")"
	record_args="$(jq -n '$ARGS.positional' --args -- \
		-target "$target" -target-port 8080 -host "$host" -scale large "${photo_record[@]}" "${extra[@]}")"
	overrides="$(jq -n --argjson cmd "$args" --arg run "$run_id" --arg digests "$digest_url" '{containerOverrides: [
		{name: "bench", command: $cmd,
		 environment: (if $digests == "" then [] else [{name: "ISUXBENCH_IMAGE_DIGESTS", value: $digests}] end)},
		{name: "uploader", environment: [{name: "RUN_ID", value: $run}]}]}')"
	started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	jq -n --arg runId "$run_id" --arg team "$TEAM" --arg stack "$STACK" --arg target "$target" --arg host "$host" \
		--arg image "$IMAGE" --arg commit "$commit" \
		--argjson benchArgs "$record_args" --arg imageDigests "$digest_record" --arg started "$started" \
		'{runId: $runId, team: ($team | tonumber), stack: $stack, target: $target, host: $host, image: $image,
		  commit: $commit, benchArgs: $benchArgs, imageDigests: $imageDigests, startedAt: $started, exitCode: null}' >"$dir/meta.json"

	local hosts=""
	if [[ "${HOST_STATS:-1}" != 0 ]]; then
		hosts="$(team_hosts "$team")"
		if [[ -n "$hosts" ]]; then
			log "recording stats on $(cut -d' ' -f2 <<<"$hosts" | paste -sd, -)"
			# shellcheck disable=SC2046
			start_host_stats "$run_id" $(cut -d' ' -f1 <<<"$hosts") || log "failed to start stats on the team servers"
		fi
	fi

	log "run $run_id: $IMAGE -> $host via $target"
	local task_flags=()
	[[ -z "${BENCH_RUN_REVISION:-}" ]] || task_flags=(--skip-task-definition --revision "$BENCH_RUN_REVISION")
	set +e
	(
		cd "$RUNNER_DIR/ecspresso"
		export BENCH_RUNNER_INFRA="$INFRA" BENCH_IMAGE="$IMAGE"
		# ecspresso は [DEBUG] のメッセージ（タスク定義の JSON を複数行で含む）も出すので、次の時刻の行まで省く
		ecspresso run --no-color --config ecspresso.jsonnet --overrides "$overrides" --watch-container bench \
			--tags "Project=isucon2026,BenchRun=$run_id" "${task_flags[@]}" 2>&1 |
			awk '/^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T/ { skip = /\[DEBUG\]/ } !skip' >&2
	)
	set -e
	if [[ -n "$hosts" ]]; then
		collect_host_stats "$run_id" "$dir" "$hosts" || log "failed to collect stats from the team servers"
		save_ebs_limits "$dir" "$hosts" || log "failed to read the EBS limits"
	fi
	aws s3 cp --recursive --only-show-errors "s3://$BUCKET/runs/$run_id/" "$dir/" || log "failed to download results"
	rc="$(cat "$dir/exit-code" 2>/dev/null || echo 2)"
	jq --argjson rc "$rc" --arg finished "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.exitCode = $rc | .finishedAt = $finished' \
		"$dir/meta.json" >"$dir/meta.json.tmp" && mv "$dir/meta.json.tmp" "$dir/meta.json"
	if [[ -s "$dir/result.json" ]]; then
		[[ -e "$RESULTS_DIR/runs.tsv" ]] || printf 'run\tteam\tcommit\tpass\tscore\targs\n' >"$RESULTS_DIR/runs.tsv"
		jq -r --slurpfile meta "$dir/meta.json" \
			'[$meta[0].runId, $meta[0].team, $meta[0].commit, .pass, .score, ($meta[0].benchArgs | join(" "))] | @tsv' \
			"$dir/result.json" >>"$RESULTS_DIR/runs.tsv"
	fi
	if [[ -n "${BENCH_RUN_PARENT:-}" ]]; then
		# 並行実行では親がまとめて表示するので、要約はファイルに書くだけにする
		if [[ -e "$dir/stats.tsv" || -d "$dir/hosts" ]]; then
			python3 "$RUNNER_DIR/report.py" "$dir" >"$dir/report.txt"
		fi
		log "finished $run_id (exit $rc)"
	else
		show "$dir"
	fi
	return "$rc"
}

cmd_show() {
	local run_id="${1:-}"
	if [[ -z "$run_id" ]]; then
		[[ -d "$RESULTS_DIR" ]] && run_id="$(find "$RESULTS_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort | tail -1)"
		[[ -n "$run_id" ]] || die "no results in $RESULTS_DIR"
	fi
	[[ -d "$RESULTS_DIR/$run_id" ]] || die "no results for $run_id"
	show "$RESULTS_DIR/$run_id"
}

case "${1:-run}" in
run) cmd_run ;;
image)
	load_infra
	build_and_push
	echo "$IMAGE"
	;;
show) cmd_show "${2:-}" ;;
*) die "usage: $0 [run|image|show [RUN_ID]]" ;;
esac
