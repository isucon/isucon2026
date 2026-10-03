This repository is used to develop an ISUCON challenge, where teams optimize a
web application on three identical VMs to maximize its benchmark scor

## Infrastructure changes

- Reflect all infrastructure and runtime configuration changes in Pulumi code or
  the target stack configuration. Do not leave changes only in AWS console/CLI
  settings or generated S3 deployment configuration.
- Preserve the existing ownership boundary: Pulumi manages infrastructure and
  deployment configuration; ecspresso manages ECS services and task definitions.
  Apply Pulumi changes and redeploy affected services through ecspresso.
- Keep secret values in Secrets Manager. Record only secret key names and
  references in Pulumi and deployment definitions; never commit secret values.
- If an emergency change is made directly in AWS, reconcile it into Pulumi and
  ecspresso configuration during the same task and verify that subsequent
  deployments preserve it.
