# Register existing Pulumi environments

`provisioning/aws/Pulumi.dev.yaml` maps physical EC2 team numbers to portal team
IDs through `portalTeamIds`. Apply the EC2 tag changes with Pulumi before syncing.
The worker discovers `Project=isucon2026` and `Role=contestant` or `Role=router`,
using `IsuconTeamID` and `IsuconInstanceNum` (1–4 servers, 5 internal Router).
Router tags also carry `IsuconBenchmarkHost`, preserving the physical team's
hostname when its portal team ID differs.

After deploying the portal migration and worker image, run an ECS worker task
through ecspresso with this override:

```json
{"containerOverrides":[{"name":"worker","command":["worker","--sync-instances=isucon2026"]}]}
```

For the initial portal-dev import only, add `--create-trial-teams=8,9,10,11,12`.
This explicitly creates hidden trial teams without contestant accounts. An
occupied ID with another name is rejected. Repeating the command preserves team
and server IDs. It does not run automatically or create teams during discovery.

Stopped EC2 instances have STOPPED status and no public address. Routers are
stored separately and never appear in portal server lists. CloudFormation
self-registration continues to use its own path and resets any legacy hostname.

Before applying Pulumi, inspect the preview for replacements and instance type
changes. Keep the stack's AMIs and per-team Router sizes aligned with the existing
instances; registration only needs tag updates.

EC2 reports `associatePublicIpAddress=false` when stopped. Treat this setting as
launch-only with `ignoreChanges`, so syncing tags or reapplying the stack does
not replace stopped instances merely because their ephemeral addresses vanished.
