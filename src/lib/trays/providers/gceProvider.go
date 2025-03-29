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
)

type GceProvider struct {
	ITrayProvider
	Name   string
	config config.ProviderConfig

	instanceClient *compute.InstancesClient
}

func NewGceProvider(name string, providerConfig config.ProviderConfig) *GceProvider {
	var provider = &GceProvider{}

	provider.Name = name
	provider.config = providerConfig

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

	var zone = "europe-west1-c"

	insert, err := instancesClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: "parity-ci-2024",
		Zone:    zone,
		InstanceResource: &computepb.Instance{
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{
					{
						Key:   proto.String("startup-script"),
						Value: proto.String("#! /bin/bash\napt-get update\napt-get install -y nginx\n"),
					},
				},
			},
			Scheduling: &computepb.Scheduling{
				InstanceTerminationAction: proto.String(computepb.Scheduling_DELETE.String()),
				Preemptible:               proto.Bool(true),
				ProvisioningModel:         proto.String(computepb.Scheduling_SPOT.String()),
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
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, "e2-micro")),
			Name:        proto.String(tray.Name),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{
					Network:    proto.String("https://www.googleapis.com/compute/v1/projects/parity-ci-2024/global/networks/parity-ci-stg"),
					Subnetwork: proto.String("https://www.googleapis.com/compute/v1/projects/parity-ci-2024/regions/europe-west1/subnetworks/parity-ci-europe-west1-stg"),
				},
			},
			Tags: &computepb.Tags{
				Items: []string{"iap-22-tcp-stg", "int-mig-gke-deny-stg"},
			},
		},
	})
	if err != nil {
		logger.Errorf("Error creating tray: %v", err)
		return err
	}

	logger.Infof("Created tray: %v", insert)

	return nil
}

func (g GceProvider) CleanTray(id string) error {
	client, err := g.createInstancesClient()
	if err != nil {
		return err
	}

	_, err = client.Delete(context.Background(), &computepb.DeleteInstanceRequest{
		Instance: id,
		Project:  "parity-ci-2024",
		Zone:     "europe-west1-c",
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
	instancesClient, err := compute.NewInstancesRESTClient(ctx, option.WithCredentialsFile("parity-ci-2024-6f2e1072e896.json"))

	if err == nil {
		g.instanceClient = instancesClient
	}

	return instancesClient, err
}

var startupScript = `#! /bin/bash
apt-get update && apt-get install -y git dotnet-runtime-8.0 golang-go tar curl

curl -sL -o actions-runner-linux-x64-2.323.0.tar.gz https://github.com/actions/runner/releases/download/v2.323.0/actions-runner-linux-x64-2.323.0.tar.gz
ls -al
tar xzf ./actions-runner-linux-x64-2.323.0.tar.gz
`
