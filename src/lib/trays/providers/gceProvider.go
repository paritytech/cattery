package providers

import (
	"cattery/lib/config"
	"cattery/lib/trays"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

type GceProvider struct {
	Name           string
	providerConfig config.ProviderConfig

	instanceClient *compute.InstancesClient

	// pendingOps tracks insert operations between StartDeploy and WaitDeploy.
	// Lost on process restart, which is acceptable: the row in the repository
	// has zone+name, so the stale handler can still clean up the VM if the
	// agent never registers.
	pendingOps sync.Map // map[trayId]*compute.Operation

	logger *logrus.Entry
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

// StartDeploy submits the Insert request to GCE. The chosen zone is written to
// ProviderData *before* the API call so that even an Insert failure leaves
// enough data to attempt cleanup (which may no-op against a 404). The returned
// operation handle is stashed for WaitDeploy.
func (g *GceProvider) StartDeploy(ctx context.Context, tray *trays.Tray) error {
	trayConfig, ok := tray.TrayConfig().(config.GoogleTrayConfig)
	if !ok {
		return fmt.Errorf("unexpected tray config type for gce provider, tray %s", tray.Id)
	}

	zones := trayConfig.Zones
	if len(zones) == 0 {
		return fmt.Errorf("no zones configured for tray %s", tray.Id)
	}
	zone := zones[rand.Intn(len(zones))]

	tray.ProviderData["zone"] = zone

	project := g.providerConfig.Get("project")
	instanceTemplate := trayConfig.InstanceTemplate
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
		g.logger.Errorf("Failed to start tray creation: %v", err)
		return err
	}

	g.pendingOps.Store(tray.Id, op)
	return nil
}

// WaitDeploy blocks until the create operation completes. If no operation is
// tracked locally (e.g. process restart between StartDeploy and WaitDeploy),
// it returns nil — the agent registration path or the stale handler will
// resolve the eventual outcome.
func (g *GceProvider) WaitDeploy(ctx context.Context, tray *trays.Tray) error {
	v, ok := g.pendingOps.LoadAndDelete(tray.Id)
	if !ok {
		g.logger.Tracef("No pending operation for tray %s; skipping wait", tray.Id)
		return nil
	}
	op := v.(*compute.Operation)
	if err := op.Wait(ctx); err != nil {
		g.logger.Errorf("Failed waiting for tray creation to complete: %v", err)
		return err
	}
	return nil
}

func (g *GceProvider) CleanTray(ctx context.Context, tray *trays.Tray) error {
	g.pendingOps.Delete(tray.Id)

	zone := tray.ProviderData["zone"]
	if zone == "" {
		g.logger.Warnf("CleanTray called without zone for tray %s; nothing to delete", tray.Id)
		return nil
	}

	client, err := g.createInstancesClient()
	if err != nil {
		return err
	}

	project := g.providerConfig.Get("project")

	_, err = client.Delete(ctx, &computepb.DeleteInstanceRequest{
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
