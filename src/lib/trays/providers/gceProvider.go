package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"errors"
	"fmt"
	"math/rand"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

type GceProvider struct {
	TrayProvider
	Name           string
	providerConfig config.ProviderConfig

	instanceClient *compute.InstancesClient
	logger         *logrus.Entry
}

func NewGceProvider(name string, providerConfig config.ProviderConfig) *GceProvider {
	provider := &GceProvider{
		Name:           name,
		providerConfig: providerConfig,
		logger:         logrus.WithFields(logrus.Fields{"name": "gceProvider"}),
	}

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

func (g *GceProvider) Close() error {
	if g.instanceClient != nil {
		return g.instanceClient.Close()
	}
	return nil
}

func (g *GceProvider) RunTray(tray *trays.Tray) error {
	ctx := context.Background()

	trayConfig, ok := tray.TrayConfig().(config.GoogleTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for gce provider, tray %s", tray.Id)
	}

	project := g.providerConfig.Get("project")
	instanceTemplate := trayConfig.InstanceTemplate
	zones := trayConfig.Zones
	machineType := trayConfig.MachineType

	var extraMetadata config.TrayExtraMetadata
	if tt := tray.TrayType(); tt != nil {
		extraMetadata = tt.ExtraMetadata
	}

	metadata := createGcpMetadata(
		map[string]string{
			"cattery-url":      config.Get().Server.AdvertiseUrl,
			"cattery-agent-id": tray.Id,
		},
		extraMetadata,
	)

	if len(zones) == 0 {
		return fmt.Errorf("no zones configured for tray %s", tray.Id)
	}

	var zone = zones[rand.Intn(len(zones))]

	op, err := g.instanceClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project:                project,
		Zone:                   zone,
		SourceInstanceTemplate: &instanceTemplate,
		InstanceResource: &computepb.Instance{
			MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType)),
			Name:        proto.String(tray.Id),
			Metadata:    metadata,
		},
	})
	if err != nil {
		g.logger.Errorf("Failed to create tray: %v", err)
		return err
	}

	if err := op.Wait(ctx); err != nil {
		g.logger.Errorf("Failed waiting for tray creation to complete: %v", err)
		return err
	}

	tray.ProviderData["zone"] = zone

	return nil
}

func (g *GceProvider) CleanTray(tray *trays.Tray) error {
	client, err := g.createInstancesClient()
	if err != nil {
		return err
	}

	zone := tray.ProviderData["zone"]
	project := g.providerConfig.Get("project")

	_, err = client.Delete(context.Background(), &computepb.DeleteInstanceRequest{
		Instance: tray.Id,
		Project:  project,
		Zone:     zone,
	})
	if err != nil {
		var e *googleapi.Error
		if errors.As(err, &e) {
			if e.Code != 404 {
				return err
			} else {
				g.logger.Tracef("Tray not found during deletion; skipping: %v (tray %s)", err, tray.Id)
				return nil
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

	if err != nil {
		return nil, err
	}

	g.instanceClient = instancesClient
	return instancesClient, nil
}

func createGcpMetadata(fieldMaps ...map[string]string) *computepb.Metadata {

	var items []*computepb.Items

	for _, fieldMap := range fieldMaps {
		if fieldMap == nil {
			continue
		}
		for k, v := range fieldMap {
			items = append(items, &computepb.Items{Key: proto.String(k), Value: proto.String(v)})
		}
	}

	return &computepb.Metadata{Items: items}
}
