import * as pulumi from "@pulumi/pulumi";
import * as z from "zod";

const c = new pulumi.Config();

// Router の 443/8080 へ接続してよい送信元（bench を動かす端末など）
const ClientCidrsSchema = z.array(z.string());

export const config = {
	serviceAmiId: c.get("serviceAmiId"),
	routerAmiId: c.get("routerAmiId"),
	availabilityZone: c.get("availabilityZone") ?? "ap-northeast-1a",
	serviceInstanceType: c.get("serviceInstanceType") ?? "c7i.large",
	routerInstanceType: c.get("routerInstanceType") ?? "c6in.large",
	clientCidrs: ClientCidrsSchema.parse(c.getObject("clientCidrs") ?? []),
	// Router の 443 で使う証明書。指定がなければ自己署名の証明書を生成する
	tlsCertificate: c.getSecret("tlsCertificate"),
	tlsPrivateKey: c.getSecret("tlsPrivateKey"),
};
