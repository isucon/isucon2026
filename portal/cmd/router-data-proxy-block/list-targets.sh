#!/usr/bin/env bash
# list-targets.sh [cluster] > targets.tsv
#
# Runs targets.sql once in a read-only transaction from a throwaway Fargate task.
# The task reuses the portal service's database, credentials, network and log group,
# and its task definition is deregistered afterwards.
set -euo pipefail

cluster="${1:-portal-production}"
here="$(cd "$(dirname "$0")" && pwd)"
sql="$(cat "$here/targets.sql")"

service="$(aws ecs describe-services --cluster "$cluster" --services portal --query 'services[0]' --output json)"
definition="$(aws ecs describe-task-definition --task-definition "$(jq -r .taskDefinition <<<"$service")" --query taskDefinition --output json)"
network="$(jq -c .networkConfiguration <<<"$service")"
container="$(jq -c '.containerDefinitions[] | select(.name == "portal")' <<<"$definition")"
env_value() { jq -er --arg name "$1" '.environment[] | select(.name == $name) | .value' <<<"$container"; }
secret() { jq -er --arg name "$1" '.secrets[] | select(.name == $name) | .valueFrom' <<<"$container"; }

host="$(env_value PGHOST)"
database="$(env_value ISUXPORTAL_DATABASE_URL | sed -E 's#^postgres(ql)?://[^/]*/([^?]+).*#\2#')"
log_group="$(jq -er '.logConfiguration.options["awslogs-group"]' <<<"$container")"
region="$(jq -er '.logConfiguration.options["awslogs-region"]' <<<"$container")"
family="$cluster-router-targets-oneoff"
prefix="router-targets-$(date -u +%Y%m%dT%H%M%S)"
echo "database $database on $host" >&2

script='wget -q -O /tmp/rds.pem https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem && printf "%s" "$SQL" | psql -X -q -A -t -v ON_ERROR_STOP=1 -P pager=off'
containers="$(jq -n --arg sql "$sql" --arg script "$script" --arg host "$host" --arg database "$database" \
	--arg user "$(secret PGUSER)" --arg password "$(secret PGPASSWORD)" \
	--arg group "$log_group" --arg region "$region" --arg prefix "$prefix" '[{
	name: "sql", image: "public.ecr.aws/docker/library/postgres:17-alpine", essential: true,
	command: ["sh", "-c", $script],
	environment: [
		{name: "PGHOST", value: $host},
		{name: "PGDATABASE", value: $database},
		{name: "PGSSLMODE", value: "verify-full"},
		{name: "PGSSLROOTCERT", value: "/tmp/rds.pem"},
		{name: "PGOPTIONS", value: "-c default_transaction_read_only=on"},
		{name: "SQL", value: $sql}],
	secrets: [{name: "PGUSER", valueFrom: $user}, {name: "PGPASSWORD", valueFrom: $password}],
	logConfiguration: {logDriver: "awslogs", options: {
		"awslogs-group": $group, "awslogs-region": $region, "awslogs-stream-prefix": $prefix}}}]')"

arn="$(aws ecs register-task-definition --family "$family" --network-mode awsvpc --requires-compatibilities FARGATE \
	--cpu 256 --memory 512 --execution-role-arn "$(jq -r .executionRoleArn <<<"$definition")" \
	--container-definitions "$containers" --query 'taskDefinition.taskDefinitionArn' --output text)"
trap 'aws ecs deregister-task-definition --task-definition "$arn" --query taskDefinition.status --output text >&2' EXIT

task="$(aws ecs run-task --cluster "$cluster" --launch-type FARGATE --task-definition "$arn" \
	--network-configuration "$(jq -c '{awsvpcConfiguration: .awsvpcConfiguration}' <<<"$network")" \
	--query 'tasks[0].taskArn' --output text)"
aws ecs wait tasks-stopped --cluster "$cluster" --tasks "$task"
stopped="$(aws ecs describe-tasks --cluster "$cluster" --tasks "$task" --query 'tasks[0]' --output json)"
events="$(aws logs get-log-events --log-group-name "$log_group" --log-stream-name "$prefix/sql/${task##*/}" \
	--start-from-head --output json)"
if [ "$(jq -r '.containers[0].exitCode' <<<"$stopped")" != 0 ] ||
	! jq -e 'any(.events[]; .message == "targets-complete")' <<<"$events" >/dev/null; then
	jq -r '.events[].message' <<<"$events" >&2
	echo "query task failed: $(jq -r '.stoppedReason' <<<"$stopped")" >&2
	exit 1
fi
jq -r '.events[].message | select(startswith("target\t")) | sub("^target\t"; "")' <<<"$events"
