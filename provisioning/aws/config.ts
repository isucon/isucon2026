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
	serviceAmiId: c.get("serviceAmiId"),
	routerAmiId: c.get("routerAmiId"),
	availabilityZone: c.get("availabilityZone") ?? "ap-northeast-1a",
	serviceInstanceType: c.get("serviceInstanceType") ?? "c7i.large",
	routerInstanceType: c.get("routerInstanceType") ?? "c6in.large",
	// db1 はディスクの IOPS が効くので、EBS の上限が高い種別（c6in.large はベースライン 6,250 IOPS）と
	// IOPS・帯域を上げた gp3 を使う。c7i.large はインスタンス側の上限が 3,600 IOPS で頭打ちになる
	databaseInstanceType: c.get("databaseInstanceType") ?? "c6in.large",
	databaseVolumeIops: c.getNumber("databaseVolumeIops") ?? 6000,
	databaseVolumeThroughput: c.getNumber("databaseVolumeThroughput") ?? 200,
	clientCidrs: ClientCidrsSchema.parse(c.getObject("clientCidrs") ?? []),
	// 一時的にサーバー（Router を除く）のパブリック IP へ直接 SSH してよい送信元。通常は空にしておく
	sshCidrs: ClientCidrsSchema.parse(c.getObject("sshCidrs") ?? []),
	// Router の 443 で使う証明書。指定がなければ自己署名の証明書を生成する
	tlsCertificate: c.getSecret("tlsCertificate"),
	tlsPrivateKey: c.getSecret("tlsPrivateKey"),
};
