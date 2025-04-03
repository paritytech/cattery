package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"fmt"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
	"strconv"
	"strings"
)

type GceProvider struct {
	ITrayProvider
	Name           string
	providerConfig config.ProviderConfig

	instanceClient *compute.InstancesClient
}

func NewGceProvider(name string, providerConfig config.ProviderConfig) *GceProvider {
	var provider = &GceProvider{}

	provider.Name = name
	provider.providerConfig = providerConfig

	provider.instanceClient = nil

	return provider
}

func (g GceProvider) GetTray(id string) (*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (g GceProvider) ListTrays() ([]*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (g GceProvider) RunTray(tray *trays.Tray) error {
	ctx := context.Background()
	instancesClient, err := g.createInstancesClient()
	if err != nil {
		return fmt.Errorf("NewInstancesRESTClient: %w", err)
	}
	defer instancesClient.Close()

	var (
		project = g.providerConfig.Get("project")

		zone           = tray.TrayConfig().Get("zone")
		machineType    = tray.TrayConfig().Get("machineType")
		tags           = strings.Split(tray.TrayConfig().Get("tags"), ",")
		preemptible, _ = strconv.ParseBool(tray.TrayConfig().Get("preemptible"))
		network        = tray.TrayConfig().Get("network")
		subnetwork     = tray.TrayConfig().Get("subnetwork")
	)

	var agentStartupCommand = fmt.Sprintf("cattery agent -i %s -s %s -r %s", tray.Id(), config.AppConfig.AdvertiseUrl, "/actions-runner")

	_, err = instancesClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: project,
		Zone:    zone,
		InstanceResource: &computepb.Instance{
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{
					{
						Key:   proto.String("startup-script"),
						Value: proto.String(startupScript + "\n" + agentStartupCommand),
					},
				},
			},
			Scheduling: &computepb.Scheduling{
				Preemptible: proto.Bool(preemptible),
			},
			Disks: []*computepb.AttachedDisk{
				{
					AutoDelete: proto.Bool(true),
					Boot:       proto.Bool(true),
					InitializeParams: &computepb.AttachedDiskInitializeParams{
						DiskSizeGb:  proto.Int64(10),
						DiskType:    proto.String(fmt.Sprintf("zones/%s/diskTypes/pd-standard", zone)),
						SourceImage: proto.String("https://www.googleapis.com/compute/v1/projects/ubuntu-os-cloud/global/images/ubuntu-2404-noble-amd64-v20250313"),
					},
					Type: proto.String(computepb.AttachedDisk_PERSISTENT.String()),
				},
			},
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType)),
			Name:        proto.String(tray.Id()),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{
					AccessConfigs: []*computepb.AccessConfig{
						{
							NetworkTier: proto.String(computepb.AccessConfig_STANDARD.String()),
						},
					},
					Network:    proto.String(network),
					Subnetwork: proto.String(subnetwork),
				},
			},
			Tags: &computepb.Tags{
				Items: tags,
			},
		},
	})
	if err != nil {
		logger.Errorf("Error creating tray: %v", err)
		return err
	}

	return nil
}

func (g GceProvider) CleanTray(tray *trays.Tray) error {
	client, err := g.createInstancesClient()
	if err != nil {
		return err
	}

	var (
		zone    = tray.TrayConfig().Get("zone")
		project = g.providerConfig.Get("project")
	)

	_, err = client.Delete(context.Background(), &computepb.DeleteInstanceRequest{
		Instance: tray.Id(),
		Project:  project,
		Zone:     zone,
	})
	if err != nil {
		return err
	}

	return nil
}

func (g GceProvider) createInstancesClient() (*compute.InstancesClient, error) {

	if g.instanceClient != nil {
		return g.instanceClient, nil
	}

	ctx := context.Background()

	var (
		instancesClient *compute.InstancesClient
		err             error
	)

	if credFile := g.providerConfig.Get("credentialsFile"); credFile != "" {
		instancesClient, err = compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile(g.providerConfig.Get("credentialsFile")))
	} else {
		instancesClient, err = compute.NewInstancesRESTClient(ctx)
	}

	if err == nil {
		g.instanceClient = instancesClient
	}

	return instancesClient, err
}

var startupScript = `#! /bin/bash
apt-get update
apt-get install -y git dotnet-runtime-8.0 golang-go tar curl

mkdir /actions-runner
cd /actions-runner

curl -sL -o actions-runner-linux-x64-2.323.0.tar.gz https://github.com/actions/runner/releases/download/v2.323.0/actions-runner-linux-x64-2.323.0.tar.gz
ls -al
tar xzf ./actions-runner-linux-x64-2.323.0.tar.gz

git clone https://github.com/paritytech/cattery.git

cd cattery/src
export GOMODCACHE=$HOME/golang/pkg/mod
export HOME=/root
go build -o /usr/local/bin/cattery
`
