# ISUCON2026 participant CloudFormation

The contest template uses the envcheck template's AZ-ID lookup, team-scoped
checker token, SSH-key download, and per-instance public addresses. It provisions
one VPC per team and the same service topology as `provisioning/aws`:

| Instance | Type | Private address | Portal server list |
| --- | --- | --- | --- |
| App1 (Asia) | c5.large | 192.168.1.10 | Yes, number 1 |
| App2 (Europe) | c7i.large | 192.168.2.10 | Yes, number 2 |
| App3 (America) | c7i.large | 192.168.3.10 | Yes, number 3 |
| Database | c5.large | 192.168.4.10 | Yes, number 4 |
| Router | c6in.large | 192.168.11.10 / 192.168.10.10 | No |

Each service has a separate subnet. Inter-service routes point at the Router's
data ENI, whose source/destination check is disabled. Server ACLs block access to
the Router's public interface, preserving the simulated inter-region network.
The database uses gp3 with 6,000 IOPS and 200 MiB/s throughput; root volume sizes
come from the AMIs. Five EIPs remain stable across instance replacement within a
stack. Router TLS uses a self-signed certificate generated on the Router; the
benchmark enters through HTTP port 8080 with the team-specific Host.

## Images and template download

Set `ISUXPORTAL_QUALIFY_AMI_ID` in the portal's Pulumi deployment configuration to
the ISUCON2026 **service** AMI. The existing environment variable name is retained.
Set `ISUXPORTAL_ROUTER_AMI_ID` to the Router AMI built by `provisioning/packer`.
The AMI must contain `isureal-router-control` and its systemd unit.
Build and select the new AMI before deploying the template that uses it; older AMIs containing only `isureal-portal` are incompatible.
Downloads embed both configured AMI IDs directly in the instance definitions;
participants do not choose AMIs through stack parameters. Both AMIs must be launchable in the participant account
and region. This change does not publish or rebuild AMIs.

The template is returned by the existing contestant CloudFormation download and
`GET /api/admin/teams/:team_id/cloud_formation?type=contest`. The returned template
contains that team's signed checker token; do not commit or share it between
teams. The envcheck AMI is unchanged.

## Registration and recreation

Cloud-init installs a root-only registration configuration and
`isucon-register.service`. On every boot, it obtains fresh instance ID, private
IP, and public IP through IMDSv2 and posts them to `/api/instance_registrations`
using the checker token. Failed requests retry, including the interval before an
EIP is attached. Units start asynchronously after cloud-final and are enabled
under cloud-init.target to avoid a boot-order cycle.

The portal updates app/database rows by `(team_id, number)`, preserving their
portal IDs when EC2 instances are recreated. Number 5 in the registration
protocol identifies the Router; it is stored only in the internal `team_routers`
table. Neither contestant nor administrator server-list APIs expose it. A
registration is not an environment-validation success and creates no envcheck
result. Registrations are rejected after the contest ends or token expiry.

New environments wait for Router registration before claiming benchmarks. A
benchmark receives the current Router IP and `--host` from the portal; the
shared Supervisor executes this target directly. Existing Pulumi environments
use [instance sync](../../deploy/ecspresso/INSTANCE_SYNC.md) to register their
Routers too. There is no static IP fallback; register the Router before benchmarking.
`ISUXBENCH_ALL_ADDRESSES` contains only the four contestant servers.

Recreating a stack replaces the addresses when its instances register. Delete
retired stacks: as with the historical self-registration flow, leaving two
stacks for the same team allows an old instance to register again on reboot.

## Deployment and verification

1. Deploy the updated shared Supervisor definition using the existing Pulumi +
   ecspresso workflow before enabling Router registration in the portal. The
   checked-in stack definition includes the new direct-target branch.
2. Deploy the portal with migration `007_team_routers.sql` applied and the service
   AMI configured. This does not modify existing participant environments.
3. Download a contest template, run `cfn-lint` against it, and create a disposable
   participant stack with the Router AMI.
4. Confirm that only four servers appear in the portal, and that benchmark jobs
   use the registered Router. Reboot/recreate instances and verify the addresses
   update. Inspect `isucon-register.service` and `isucon-router-bootstrap.service`
   in the journal if registration or network startup fails.

Local coverage includes template contracts, registration-script tests, PostgreSQL
HTTP integration tests for registration/replacement and hidden Router data, and
Supervisor proxy tests. Actual CloudFormation creation and a benchmark against a
new stack are still required before rollout. The registration service does not
implement the competition's full post-contest environment validation.

The Router attaches both its public and data ENIs in the EC2 `NetworkInterfaces`
property at launch. Do not add a separate `NetworkInterfaceAttachment`: attaching
another ENI during instance stabilization makes the requested and observed
interface lists differ. Inter-server routes depend on the Router resource.

## Administration SSH access

The contest Router purges `ec2-instance-connect` during cloud-init `bootcmd`
and refreshes the SSH service, disabling Instance Connect authentication on
every boot. This uses the Router AMI's Ubuntu package management and requires
no package download. App1, App2, App3, and Database keep Instance Connect and
are the only contest instances that download participant SSH keys from the
portal. The Router does not import participant keys; it retains the separate
administration key described below.

Both templates create `isuadmin` on all five instances using cloud-init `users`,
with a locked password, `/bin/bash`, and passwordless sudo. The default AMI user
is preserved. Contestant keys remain under `isucon`; administration keys are not
returned by the contestant SSH-key API.

ECS injects the environment's existing admin SSH secret into portal and worker:
`portal-develop/admin-ssh-key` for dev and `portal-production/admin-ssh-key` for
production. The portal derives the public key from that private key when rendering
a template. Missing or invalid keys reject template generation. Only the public
key enters user data; instances need no Secrets Manager permission. The private
key remains in the portal/worker's mode-0600 temporary file. Other services do not
receive it. Apply the Pulumi execution-role policy before deploying portal with
ecspresso. Replacing a secret requires redeploying both portal and worker.

Existing instances do not receive this change automatically. Install the matching
public key and sudo policy through an existing authorized SSH account, then verify
`sudo -n true` as `isuadmin`. Reconcile the instance's managed template before a
future recreation; changing CloudFormation UserData can stop/start instances and
must not be used as a live repair shortcut. Do not reboot to test authentication.

Embedded Python bootstrap files use cloud-init `gz+b64` decoding to keep the
CloudFormation document below the 51,200-byte direct-upload limit.
