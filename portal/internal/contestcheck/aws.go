package contestcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func CollectAWS(ctx context.Context) (Snapshot, error) {
	s := Snapshot{Version: 1, CapturedAt: time.Now().UTC()}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("ap-northeast-1"))
	if err != nil {
		return s, err
	}
	md := imds.NewFromConfig(cfg)
	identity, err := md.GetInstanceIdentityDocument(ctx, nil)
	if err != nil {
		return s, err
	}
	cfg.Region = identity.Region
	s.Self = identity.InstanceID
	ip, err := md.GetMetadata(ctx, &imds.GetMetadataInput{Path: "public-ipv4"})
	if err != nil {
		return s, err
	}
	defer ip.Content.Close()
	raw, err := io.ReadAll(ip.Content)
	if err != nil {
		return s, err
	}
	s.PublicIP = strings.TrimSpace(string(raw))
	api := ec2.NewFromConfig(cfg)
	self, err := api.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{s.Self}})
	if err != nil {
		return s, err
	}
	if len(self.Reservations) != 1 || len(self.Reservations[0].Instances) != 1 {
		return s, fmt.Errorf("cannot locate self")
	}
	for _, tag := range self.Reservations[0].Instances[0].Tags {
		if aws.ToString(tag.Key) == "IsuconTeamID" {
			s.Team = aws.ToString(tag.Value)
		}
	}
	if s.Team == "" {
		return s, fmt.Errorf("missing IsuconTeamID tag")
	}
	pages := ec2.NewDescribeInstancesPaginator(api, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("tag:IsuconTeamID"), Values: []string{s.Team}}}})
	subnets := map[string]bool{}
	vpcs := map[string]bool{}
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return s, err
		}
		for _, reservation := range page.Reservations {
			for _, v := range reservation.Instances {
				if v.State != nil && v.State.Name == types.InstanceStateNameTerminated {
					continue
				}
				i := Instance{ID: aws.ToString(v.InstanceId), VPC: aws.ToString(v.VpcId), Subnet: aws.ToString(v.SubnetId), PrivateIP: aws.ToString(v.PrivateIpAddress), Type: string(v.InstanceType), AMI: aws.ToString(v.ImageId)}
				attrs, _ := json.Marshal(map[string]any{"cpu_options": v.CpuOptions, "ebs_optimized": v.EbsOptimized, "placement": v.Placement})
				i.Attributes = string(attrs)
				for _, tag := range v.Tags {
					if aws.ToString(tag.Key) == "Name" {
						i.Name = aws.ToString(tag.Value)
					}
					if aws.ToString(tag.Key) == "IsuconInstanceNum" {
						i.Number, _ = strconv.Atoi(aws.ToString(tag.Value))
					}
				}
				if v.Placement != nil {
					i.AZ = aws.ToString(v.Placement.AvailabilityZone)
				}
				if v.State != nil {
					i.State = string(v.State.Name)
				}
				interfaces := []string{}
				for _, eni := range v.NetworkInterfaces {
					subnets[aws.ToString(eni.SubnetId)] = true
					// SG ingress on contestant servers may be edited under the manual. Router
					// groups are protected and their full rules are compared below.
					details := map[string]any{"id": eni.NetworkInterfaceId, "subnet": eni.SubnetId, "vpc": eni.VpcId, "ip": eni.PrivateIpAddress, "source_dest_check": eni.SourceDestCheck, "ipv6": eni.Ipv6Addresses}
					if eni.Attachment != nil {
						details["device_index"] = eni.Attachment.DeviceIndex
					}
					if i.Number == 5 {
						ids := []string{}
						for _, group := range eni.Groups {
							ids = append(ids, aws.ToString(group.GroupId))
						}
						sort.Strings(ids)
						groups, err := api.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: ids})
						if err != nil {
							return s, err
						}
						rules := []string{}
						for _, g := range groups.SecurityGroups {
							b, _ := json.Marshal(map[string]any{"id": g.GroupId, "ingress": g.IpPermissions, "egress": g.IpPermissionsEgress})
							rules = append(rules, string(b))
						}
						details["security_groups"] = canonicalSet(rules)
					}
					b, _ := json.Marshal(details)
					interfaces = append(interfaces, string(b))
				}
				i.Interfaces = canonicalSet(interfaces)
				i.InterfaceCount = len(interfaces)
				volumes := []string{}
				for _, mapping := range v.BlockDeviceMappings {
					if mapping.Ebs == nil {
						return s, fmt.Errorf("unexpected non-EBS block device")
					}
					out, err := api.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{aws.ToString(mapping.Ebs.VolumeId)}})
					if err != nil {
						return s, err
					}
					if len(out.Volumes) != 1 {
						return s, fmt.Errorf("volume not found")
					}
					vol := out.Volumes[0]
					b, _ := json.Marshal(map[string]any{"id": vol.VolumeId, "device": mapping.DeviceName, "size": vol.Size, "type": vol.VolumeType, "iops": vol.Iops, "throughput": vol.Throughput, "encrypted": vol.Encrypted, "delete_on_termination": mapping.Ebs.DeleteOnTermination})
					volumes = append(volumes, string(b))
				}
				i.Volumes = canonicalSet(volumes)
				i.VolumeCount = len(volumes)
				s.Instances = append(s.Instances, i)
				vpcs[i.VPC] = true
			}
		}
	}
	sort.Slice(s.Instances, func(i, j int) bool { return s.Instances[i].Number < s.Instances[j].Number })
	routes := []string{}
	acls := []string{}
	for vpc := range vpcs {
		aclPages := ec2.NewDescribeNetworkAclsPaginator(api, &ec2.DescribeNetworkAclsInput{Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
		for aclPages.HasMorePages() {
			page, err := aclPages.NextPage(ctx)
			if err != nil {
				return s, err
			}
			for _, acl := range page.NetworkAcls {
				associations := []string{}
				for _, a := range acl.Associations {
					if subnets[aws.ToString(a.SubnetId)] {
						associations = append(associations, aws.ToString(a.SubnetId))
					}
				}
				if len(associations) == 0 {
					continue
				}
				entries := []string{}
				for _, entry := range acl.Entries {
					b, _ := json.Marshal(entry)
					entries = append(entries, string(b))
				}
				b, _ := json.Marshal(map[string]any{"id": acl.NetworkAclId, "subnets": canonicalSet(associations), "entries": canonicalSet(entries)})
				acls = append(acls, string(b))
			}
		}

		pages := ec2.NewDescribeRouteTablesPaginator(api, &ec2.DescribeRouteTablesInput{Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				return s, err
			}
			for _, table := range page.RouteTables {
				relevant := false
				for _, a := range table.Associations {
					if aws.ToBool(a.Main) || subnets[aws.ToString(a.SubnetId)] {
						relevant = true
					}
				}
				if !relevant {
					continue
				}
				entries := []string{}
				for _, route := range table.Routes {
					b, _ := json.Marshal(route)
					entries = append(entries, string(b))
				}
				associations := []string{}
				for _, a := range table.Associations {
					if subnets[aws.ToString(a.SubnetId)] || aws.ToBool(a.Main) {
						b, _ := json.Marshal(a)
						associations = append(associations, string(b))
					}
				}
				b, _ := json.Marshal(map[string]any{"id": table.RouteTableId, "vpc": table.VpcId, "routes": canonicalSet(entries), "associations": canonicalSet(associations)})
				routes = append(routes, string(b))
			}
		}
	}
	s.Routes = canonicalSet(routes)
	s.ACLs = canonicalSet(acls)
	return s, nil
}
