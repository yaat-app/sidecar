package detection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// CloudProvider represents detected cloud provider information
type CloudProvider struct {
	Provider     string            // aws, gcp, azure, unknown
	Region       string            // e.g., us-east-1
	Zone         string            // e.g., us-east-1a
	InstanceType string            // e.g., t3.micro
	InstanceID   string            // unique instance identifier
	Tags         map[string]string // provider-specific tags
}

// DetectCloudProvider attempts to detect the cloud provider and metadata
func DetectCloudProvider() *CloudProvider {
	// Try AWS first (most common)
	if cloud := detectAWS(); cloud != nil {
		return cloud
	}

	// Try GCP
	if cloud := detectGCP(); cloud != nil {
		return cloud
	}

	// Try Azure
	if cloud := detectAzure(); cloud != nil {
		return cloud
	}

	// Not in cloud or detection failed
	return &CloudProvider{
		Provider: "unknown",
		Tags:     make(map[string]string),
	}
}

// detectAWS queries EC2 metadata service
func detectAWS() *CloudProvider {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Try to get instance identity document
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return nil
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var doc struct {
		InstanceID       string `json:"instanceId"`
		InstanceType     string `json:"instanceType"`
		Region           string `json:"region"`
		AvailabilityZone string `json:"availabilityZone"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}

	return &CloudProvider{
		Provider:     "aws",
		Region:       doc.Region,
		Zone:         doc.AvailabilityZone,
		InstanceType: doc.InstanceType,
		InstanceID:   doc.InstanceID,
		Tags: map[string]string{
			"cloud.provider":      "aws",
			"cloud.region":        doc.Region,
			"cloud.zone":          doc.AvailabilityZone,
			"cloud.instance_type": doc.InstanceType,
			"cloud.instance_id":   doc.InstanceID,
		},
	}
}

// detectGCP queries GCP metadata service
func detectGCP() *CloudProvider {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// GCP requires Metadata-Flavor header
	req, err := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/?recursive=true", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Metadata-Flavor", "Google")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var doc struct {
		ID           int64  `json:"id"`
		MachineType  string `json:"machineType"`
		Zone         string `json:"zone"`
		Name         string `json:"name"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}

	// Extract region from zone (e.g., projects/123/zones/us-central1-a -> us-central1)
	zone := doc.Zone
	if idx := strings.LastIndex(zone, "/"); idx != -1 {
		zone = zone[idx+1:]
	}
	region := zone
	if idx := strings.LastIndex(zone, "-"); idx != -1 {
		region = zone[:idx]
	}

	// Extract machine type (e.g., projects/123/machineTypes/n1-standard-1 -> n1-standard-1)
	machineType := doc.MachineType
	if idx := strings.LastIndex(machineType, "/"); idx != -1 {
		machineType = machineType[idx+1:]
	}

	return &CloudProvider{
		Provider:     "gcp",
		Region:       region,
		Zone:         zone,
		InstanceType: machineType,
		InstanceID:   doc.Name,
		Tags: map[string]string{
			"cloud.provider":      "gcp",
			"cloud.region":        region,
			"cloud.zone":          zone,
			"cloud.instance_type": machineType,
			"cloud.instance_id":   doc.Name,
		},
	}
}

// detectAzure queries Azure Instance Metadata Service
func detectAzure() *CloudProvider {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/instance?api-version=2021-02-01", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Metadata", "true")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var doc struct {
		Compute struct {
			VMSize            string `json:"vmSize"`
			Location          string `json:"location"`
			Zone              string `json:"zone"`
			Name              string `json:"name"`
			VMID              string `json:"vmId"`
			ResourceGroupName string `json:"resourceGroupName"`
		} `json:"compute"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}

	return &CloudProvider{
		Provider:     "azure",
		Region:       doc.Compute.Location,
		Zone:         doc.Compute.Zone,
		InstanceType: doc.Compute.VMSize,
		InstanceID:   doc.Compute.VMID,
		Tags: map[string]string{
			"cloud.provider":       "azure",
			"cloud.region":         doc.Compute.Location,
			"cloud.zone":           doc.Compute.Zone,
			"cloud.instance_type":  doc.Compute.VMSize,
			"cloud.instance_id":    doc.Compute.VMID,
			"cloud.resource_group": doc.Compute.ResourceGroupName,
		},
	}
}
