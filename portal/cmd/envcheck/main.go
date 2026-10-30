// envcheck verifies the participant's AWS environment and reports it to the portal.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type credentials struct {
	Host  string `json:"host"`
	Token string `json:"token"`
}
type checkInfo struct {
	AMI string `json:"ami_id"`
	AZ  string `json:"az_id"`
}
type result struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Passed       bool   `json:"passed"`
	IPAddress    string `json:"ip_address"`
	Message      string `json:"message"`
	AdminMessage string `json:"admin_message"`
	RawData      string `json:"raw_data"`
}
type instance struct {
	ID             string `json:"instance_id"`
	Name           string `json:"name"`
	Type           string `json:"instance_type"`
	AMI            string `json:"ami_id"`
	AZ             string `json:"az_id"`
	State          string `json:"state"`
	VolumeCount    int    `json:"volume_count"`
	VolumeSize     int32  `json:"volume_size"`
	VolumeType     string `json:"volume_type"`
	InterfaceCount int    `json:"interface_count"`
}
type environment struct {
	InstanceID string     `json:"instance_id"`
	PublicIP   string     `json:"public_ip"`
	Instances  []instance `json:"instances"`
}
type portal struct {
	client      *http.Client
	credentials credentials
}

func (p portal) info(ctx context.Context, name string) (checkInfo, error) {
	q := url.Values{"token": {p.credentials.Token}, "name": {name}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.credentials.Host+"/api/env_check_info?"+q.Encode(), nil)
	if err != nil {
		return checkInfo{}, errors.New("invalid portal host")
	}
	res, err := p.client.Do(req)
	if err != nil {
		return checkInfo{}, errors.New("portal info request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return checkInfo{}, fmt.Errorf("portal info HTTP %d", res.StatusCode)
	}
	var info checkInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return info, err
	}
	if info.AMI == "" || info.AZ == "" {
		return info, errors.New("portal AMI or AZ is not configured")
	}
	return info, nil
}
func (p portal) send(ctx context.Context, r result) error {
	r.Token = p.credentials.Token
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+p.credentials.Host+"/api/env_checks", bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid portal host")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return errors.New("portal result request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("portal result HTTP %d", res.StatusCode)
	}
	return nil
}

// envcheckTypes is the instance type of isucon2026-envcheck-001 … 005, matching
// EnvCheckInstances in portal/internal/cloudformation.
var envcheckTypes = [5]string{"c5.large", "c7i.large", "c7i.large", "c5.large", "c6in.large"}

func validate(e environment, info checkInfo) []string {
	var failures []string
	if len(e.Instances) != 5 {
		failures = append(failures, fmt.Sprintf("expected 5 instances, found %d", len(e.Instances)))
	}
	seen := map[string]bool{}
	foundSelf := false
	for _, i := range e.Instances {
		if i.ID == e.InstanceID {
			foundSelf = true
		}
		expectedType := ""
		for n := 1; n <= 5; n++ {
			if i.Name == fmt.Sprintf("isucon2026-envcheck-%03d", n) {
				expectedType = envcheckTypes[n-1]
			}
		}
		if expectedType == "" || seen[i.Name] {
			failures = append(failures, i.ID+": unexpected or duplicate name")
		}
		seen[i.Name] = true
		if i.Type != expectedType {
			failures = append(failures, i.ID+": unexpected instance type")
		}
		if i.AMI != info.AMI || i.AZ != info.AZ {
			failures = append(failures, i.ID+": unexpected AMI or AZ")
		}
		if i.State != "running" {
			failures = append(failures, i.ID+": instance is not running")
		}
		if i.VolumeCount != 1 || i.VolumeSize != 8 || i.VolumeType != "gp3" {
			failures = append(failures, i.ID+": expected one 8 GiB gp3 volume")
		}
		if i.InterfaceCount != 1 {
			failures = append(failures, i.ID+": expected one network interface")
		}
	}
	if !foundSelf {
		failures = append(failures, "reporting instance is not in the environment")
	}
	return failures
}

func collect(ctx context.Context) (environment, error) {
	var e environment
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("ap-northeast-1"))
	if err != nil {
		return e, err
	}
	md := imds.NewFromConfig(cfg)
	identity, err := md.GetInstanceIdentityDocument(ctx, nil)
	if err != nil {
		return e, fmt.Errorf("instance identity: %w", err)
	}
	e.InstanceID = identity.InstanceID
	publicIP, err := md.GetMetadata(ctx, &imds.GetMetadataInput{Path: "public-ipv4"})
	if err != nil {
		return e, fmt.Errorf("public IPv4: %w", err)
	}
	defer publicIP.Content.Close()
	rawIP, err := io.ReadAll(publicIP.Content)
	if err != nil {
		return e, err
	}
	e.PublicIP = strings.TrimSpace(string(rawIP))
	cfg.Region = identity.Region
	client := ec2.NewFromConfig(cfg)
	self, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{e.InstanceID}})
	if err != nil {
		return e, err
	}
	if len(self.Reservations) != 1 || len(self.Reservations[0].Instances) != 1 {
		return e, errors.New("cannot locate reporting instance")
	}
	vpc := self.Reservations[0].Instances[0].VpcId
	if vpc == nil {
		return e, errors.New("reporting instance has no VPC")
	}
	zones, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return e, err
	}
	zoneIDs := map[string]string{}
	for _, z := range zones.AvailabilityZones {
		zoneIDs[aws.ToString(z.ZoneName)] = aws.ToString(z.ZoneId)
	}
	pages := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{*vpc}}}})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return e, err
		}
		for _, r := range page.Reservations {
			for _, v := range r.Instances {
				if v.State != nil && v.State.Name == types.InstanceStateNameTerminated {
					continue
				}
				i := instance{ID: aws.ToString(v.InstanceId), Type: string(v.InstanceType), AMI: aws.ToString(v.ImageId), VolumeCount: len(v.BlockDeviceMappings), InterfaceCount: len(v.NetworkInterfaces)}
				for _, tag := range v.Tags {
					if aws.ToString(tag.Key) == "Name" {
						i.Name = aws.ToString(tag.Value)
					}
				}
				if v.Placement != nil {
					i.AZ = zoneIDs[aws.ToString(v.Placement.AvailabilityZone)]
				}
				if v.State != nil {
					i.State = string(v.State.Name)
				}
				if len(v.BlockDeviceMappings) == 1 && v.BlockDeviceMappings[0].Ebs != nil {
					volume, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{aws.ToString(v.BlockDeviceMappings[0].Ebs.VolumeId)}})
					if err != nil {
						return e, err
					}
					if len(volume.Volumes) == 1 {
						i.VolumeSize = aws.ToInt32(volume.Volumes[0].Size)
						i.VolumeType = string(volume.Volumes[0].VolumeType)
					}
				}
				e.Instances = append(e.Instances, i)
			}
		}
	}
	return e, nil
}

func run(ctx context.Context, name string) error {
	f, err := os.Open("/opt/isucon-env-checker/portal_credentials.json")
	if err != nil {
		return err
	}
	defer f.Close()
	var creds credentials
	if err := json.NewDecoder(f).Decode(&creds); err != nil {
		return err
	}
	if creds.Host == "" || creds.Token == "" {
		return errors.New("portal credentials are incomplete")
	}
	p := portal{client: &http.Client{Timeout: 15 * time.Second}, credentials: creds}
	info, err := p.info(ctx, name)
	if err != nil {
		return err
	}
	e, err := collect(ctx)
	if err != nil {
		return fmt.Errorf("AWS environment: %w", err)
	}
	failures := validate(e, info)
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	r := result{Name: name, Passed: len(failures) == 0, IPAddress: e.PublicIP, RawData: string(raw), Message: "全てのチェックをパスしました"}
	if !r.Passed {
		r.Message = strings.Join(failures, "\n")
	}
	if err := p.send(ctx, r); err != nil {
		return err
	}
	fmt.Println(r.Message)
	if !r.Passed {
		return errors.New("environment check failed")
	}
	return nil
}
func main() {
	name := "test-ssh"
	if len(os.Args) == 2 && os.Args[1] == "boot" {
		name = "test-boot"
	} else if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: isucon-env-checker [boot]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, name); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
