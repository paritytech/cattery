package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

type GceProvider struct {
	ITrayProvider
	Name           string
	providerConfig config.ProviderConfig

	instanceClient *compute.InstancesClient
	logger         *logrus.Entry
}

func NewGceProvider(name string, providerConfig config.ProviderConfig) *GceProvider {
	var provider = &GceProvider{}

	provider.Name = name
	provider.providerConfig = providerConfig

	provider.instanceClient = nil
	provider.logger = logrus.WithFields(logrus.Fields{name: "gceProvider"})

	client, err := provider.createInstancesClient()
	if err != nil {
		return nil
	}
	provider.instanceClient = client

	return provider
}

func (g *GceProvider) GetProviderName() string {
	return g.Name
}

func (g *GceProvider) GetTray(id string) (*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (g *GceProvider) ListTrays() ([]*trays.Tray, error) {
	//TODO implement me
	panic("implement me")
}

func (g *GceProvider) RunTray(tray *trays.Tray) error {
	ctx := context.Background()

	var (
		project          = g.providerConfig.Get("project")
		instanceTemplate = tray.GetTrayConfig().Get("instanceTemplate")
		zone             = tray.GetTrayConfig().Get("zone")
		machineType      = tray.GetTrayConfig().Get("machineType")
	)

	_, err := g.instanceClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project:                project,
		Zone:                   zone,
		SourceInstanceTemplate: &instanceTemplate,
		InstanceResource: &computepb.Instance{
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType)),
			Name:        proto.String(tray.GetId()),
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{
					{
						Key:   proto.String("cattery-url"),
						Value: proto.String(config.AppConfig.Server.AdvertiseUrl),
					},
					{
						Key:   proto.String("cattery-agent-id"),
						Value: proto.String(tray.GetId()),
					},
				},
			},
		},
	})
	if err != nil {
		g.logger.Errorf("Error creating tray: %v", err)
		return err
	}

	return nil
}

func (g *GceProvider) CleanTray(tray *trays.Tray) error {
	client, err := g.createInstancesClient()
	if err != nil {
		return err
	}

	var (
		zone    = tray.GetTrayConfig().Get("zone")
		project = g.providerConfig.Get("project")
	)

	_, err = client.Delete(context.Background(), &computepb.DeleteInstanceRequest{
		Instance: tray.GetId(),
		Project:  project,
		Zone:     zone,
	})
	if err != nil {
		var e *googleapi.Error
		if errors.As(err, &e) {
			if e.Code != 404 {
				return err
			} else {
				g.logger.Tracef("Tray deletion error, tray %s not found: %v", tray.GetId(), err)
			}
		}
		return err
	}

	return nil
}

func (g *GceProvider) createInstancesClient() (*compute.InstancesClient, error) {

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

/*
vr startupScript = `#! /bin/bash
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
*/
