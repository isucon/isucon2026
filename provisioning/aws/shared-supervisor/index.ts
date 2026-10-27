import * as pulumi from "@pulumi/pulumi";

// 共有プールの ecspresso 用の定義（タスク定義、サービス定義、ecspresso の設定）を stack の設定に保持し、
// そのまま出力する。リソースは作らない。render.py が出力から ecspresso の JSON を書く。
export const deployments = new pulumi.Config().requireObject("deployments");
