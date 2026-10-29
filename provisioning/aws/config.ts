import * as pulumi from "@pulumi/pulumi";
import * as z from "zod";
import { maxTeams } from "./lib/topology";

const c = new pulumi.Config();

// Router の 443/8080 へ接続してよい送信元（bench を動かす端末など）
const ClientCidrsSchema = z.array(z.string());

export const config = {
	// 同じ VPC に作る環境（チーム）の数
	teamCount: z
		.number()
		.int()
		.min(1)
		.max(maxTeams)
		.parse(c.getNumber("teamCount") ?? 1),
	// Keep team networks but omit EC2 instances for retired trial teams.
	disabledInstanceTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject<number[]>("disabledInstanceTeams") ?? []),
	// Give additional teams their own certificate without changing shared TLS.
	independentTlsTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject<number[]>("independentTlsTeams") ?? []),
	teamAdminSshPublicKeys: z
		.record(z.string(), z.array(z.string().min(1)))
		.parse(c.getObject("teamAdminSshPublicKeys") ?? {}),
	guardTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject<number[]>("guardTeams") ?? []),
	// A stopped guard stays disabled across instance replacements through SSM.
	guardStoppedTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject<number[]>("guardStoppedTeams") ?? []),
	// 使っていないチームのインスタンスを、ディスクごと残したまま止める
	stoppedTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject<number[]>("stoppedTeams") ?? []),
	protectedDatabaseTeams: z
		.array(z.number().int().min(1).max(maxTeams))
		.parse(c.getObject("protectedDatabaseTeams") ?? []),
	portalTeamIds: z
		.record(z.string(), z.number().int().positive())
		.parse(c.getObject("portalTeamIds") ?? {}),
	teamSshPublicKeys: z
		.record(z.string(), z.array(z.string().min(1)))
		.parse(c.getObject("teamSshPublicKeys") ?? {}),
	// Pin new environments independently, without replacing existing teams.
	teamImages: z
		.record(
			z.string(),
			z.object({
				serviceAmiId: z.string().min(1),
				routerAmiId: z.string().min(1),
			}),
		)
		.parse(c.getObject("teamImages") ?? {}),
	teamRouterInstanceTypes: z
		.record(z.string(), z.string().min(1))
		.parse(c.getObject("teamRouterInstanceTypes") ?? {}),
	// チームのサーバーごとの種別（{"9": {"app2": "c6i.large"}}）。指定がなければ asiaInstanceType、
	// serviceInstanceType か databaseInstanceType
	teamServerInstanceTypes: z
		.record(
			z.string(),
			z.partialRecord(
				z.enum(["app1", "app2", "app3", "db1"]),
				z.string().min(1),
			),
		)
		.parse(c.getObject("teamServerInstanceTypes") ?? {}),
	serviceAmiId: c.get("serviceAmiId"),
	routerAmiId: c.get("routerAmiId"),
	availabilityZone: c.get("availabilityZone") ?? "ap-northeast-1a",
	// asia の app（app1）と db1 は c5.large、海外の app は c7i.large。c5.large には SHA-NI がない
	serviceInstanceType: c.get("serviceInstanceType") ?? "c7i.large",
	asiaInstanceType: c.get("asiaInstanceType") ?? "c5.large",
	routerInstanceType: c.get("routerInstanceType") ?? "c6in.large",
	// db1 の gp3 は IOPS・帯域を上げておく。c5.large のベースラインは 4,000 IOPS・81 MB/s で、
	// それを超える分はインスタンスのバースト（1 日 30 分ほど）の間だけ使える
	databaseInstanceType: c.get("databaseInstanceType") ?? "c5.large",
	databaseVolumeIops: c.getNumber("databaseVolumeIops") ?? 6000,
	databaseVolumeThroughput: c.getNumber("databaseVolumeThroughput") ?? 200,
	clientCidrs: ClientCidrsSchema.parse(c.getObject("clientCidrs") ?? []),
	// 一時的にサーバー（Router を除く）のパブリック IP へ直接 SSH してよい送信元。通常は空にしておく
	sshCidrs: ClientCidrsSchema.parse(c.getObject("sshCidrs") ?? []),
	// Router の 443 で使う証明書。指定がなければ自己署名の証明書を生成する
	tlsCertificate: c.getSecret("tlsCertificate"),
	tlsPrivateKey: c.getSecret("tlsPrivateKey"),
};
