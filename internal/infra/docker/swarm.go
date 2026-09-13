package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
)

// SwarmTokens holds the join tokens for workers and managers.
type SwarmTokens struct {
	NodeID       string `json:"node_id"`
	WorkerToken  string `json:"worker_token"`
	ManagerToken string `json:"manager_token"`
}

// SwarmNodeSummary represents a brief overview of a node in the swarm.
type SwarmNodeSummary struct {
	ID            string `json:"id"`
	Hostname      string `json:"hostname"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	Availability  string `json:"availability"`
	ManagerLeader bool   `json:"manager_leader"`
}

// SwarmServiceSummary represents a brief overview of a service in the swarm.
type SwarmServiceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	Replicas  uint64 `json:"replicas"`
	Ports     string `json:"ports"`
	CreatedAt string `json:"created_at"`
}

// SwarmInit initializes a new Docker Swarm on this node advertising on the private VPN IP.
func SwarmInit(ctx context.Context, advertiseVPNIP string, overlayMTU int) (*SwarmTokens, error) {
	cli, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	listenAddr := "0.0.0.0:2377"
	advertiseAddr := advertiseVPNIP
	if advertiseAddr == "" {
		advertiseAddr = "0.0.0.0"
	}

	req := swarm.InitRequest{
		ListenAddr:    listenAddr,
		AdvertiseAddr: advertiseAddr,
		DataPathPort:  4789,
	}

	nodeID, err := cli.SwarmInit(ctx, req)
	if err != nil {
		// If already in a swarm, attempt inspect to get tokens
		if strings.Contains(err.Error(), "already part of a swarm") {
			return GetSwarmTokens(ctx)
		}
		return nil, fmt.Errorf("swarm init failed: %w", err)
	}

	// Create default overlay network with optimized MTU for WireGuard (1200)
	if overlayMTU <= 0 {
		overlayMTU = 1200
	}
	_ = CreateOverlayNetwork(ctx, "autohost-net", overlayMTU)

	// Set task history retention limit to 0 so scaled-down/unused replica containers are immediately deleted
	if sw, err := cli.SwarmInspect(ctx); err == nil {
		zero := int64(0)
		sw.Spec.Orchestration.TaskHistoryRetentionLimit = &zero
		_ = cli.SwarmUpdate(ctx, sw.Version, sw.Spec, swarm.UpdateFlags{})
	}

	tokens, err := GetSwarmTokens(ctx)
	if err != nil {
		return &SwarmTokens{NodeID: nodeID}, nil
	}
	tokens.NodeID = nodeID
	return tokens, nil
}

// GetSwarmTokens inspects the swarm to retrieve worker and manager join tokens.
func GetSwarmTokens(ctx context.Context) (*SwarmTokens, error) {
	cli, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	sw, err := cli.SwarmInspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("swarm inspect: %w", err)
	}

	nodeID := sw.ID
	return &SwarmTokens{
		NodeID:       nodeID,
		WorkerToken:  sw.JoinTokens.Worker,
		ManagerToken: sw.JoinTokens.Manager,
	}, nil
}

// SwarmJoin joins this node to an existing Swarm cluster.
func SwarmJoin(ctx context.Context, managerVPNIP, token, advertiseVPNIP string) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	listenAddr := "0.0.0.0:2377"
	advertiseAddr := advertiseVPNIP
	if advertiseAddr == "" {
		advertiseAddr = "0.0.0.0"
	}

	remoteAddr := managerVPNIP
	if !strings.Contains(remoteAddr, ":") {
		remoteAddr = fmt.Sprintf("%s:2377", remoteAddr)
	}

	req := swarm.JoinRequest{
		ListenAddr:    listenAddr,
		AdvertiseAddr: advertiseAddr,
		RemoteAddrs:   []string{remoteAddr},
		JoinToken:     token,
	}

	if err := cli.SwarmJoin(ctx, req); err != nil {
		if strings.Contains(err.Error(), "already part of a swarm") {
			return nil
		}
		return fmt.Errorf("swarm join failed: %w", err)
	}
	return nil
}

// SwarmLeave leaves the current swarm cluster.
func SwarmLeave(ctx context.Context, force bool) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	return cli.SwarmLeave(ctx, force)
}

// CreateOverlayNetwork creates an attachable overlay network with custom MTU.
func CreateOverlayNetwork(ctx context.Context, name string, mtu int) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	if mtu <= 0 {
		mtu = 1200
	}

	opts := types.NetworkCreate{
		Driver:     "overlay",
		Attachable: true,
		Options: map[string]string{
			"com.docker.network.driver.overlay.mtu": strconv.Itoa(mtu),
			"com.docker.network.mtu":                strconv.Itoa(mtu),
		},
	}

	_, err = cli.NetworkCreate(ctx, name, opts)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create overlay network %s: %w", name, err)
	}
	return nil
}

// DeployServiceSpec defines the input to create or update a stateless service in Swarm.
type DeployServiceSpec struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Ports       string            `json:"ports"` // e.g. "80:8080" or "8080"
	EnvVars     map[string]string `json:"env_vars"`
	Replicas    uint64            `json:"replicas"`
	NetworkName string            `json:"network_name"`
}

// DeploySwarmService creates or updates a service in Docker Swarm.
func DeploySwarmService(ctx context.Context, spec DeployServiceSpec) (string, error) {
	cli, err := GetClient()
	if err != nil {
		return "", fmt.Errorf("docker client: %w", err)
	}

	if spec.Replicas == 0 {
		spec.Replicas = 1
	}
	if spec.NetworkName == "" {
		spec.NetworkName = "autohost-net"
	}

	var envList []string
	for k, v := range spec.EnvVars {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	var portConfigs []swarm.PortConfig
	if spec.Ports != "" {
		parts := strings.Split(spec.Ports, ":")
		if len(parts) == 2 {
			pub, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
			tgt, err2 := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
			if err1 == nil && err2 == nil {
				portConfigs = append(portConfigs, swarm.PortConfig{
					Protocol:      swarm.PortConfigProtocolTCP,
					TargetPort:    uint32(tgt),
					PublishedPort: uint32(pub),
					PublishMode:   swarm.PortConfigPublishModeIngress,
				})
			}
		} else if len(parts) == 1 {
			p, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
			if err1 == nil {
				portConfigs = append(portConfigs, swarm.PortConfig{
					Protocol:      swarm.PortConfigProtocolTCP,
					TargetPort:    uint32(p),
					PublishedPort: uint32(p),
					PublishMode:   swarm.PortConfigPublishModeIngress,
				})
			}
		}
	}

	serviceSpec := swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name: spec.Name,
			Labels: map[string]string{
				"autohost.service": "true",
				"autohost.name":    spec.Name,
			},
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image: spec.Image,
				Env:   envList,
				Labels: map[string]string{
					"autohost.service": spec.Name,
				},
			},
			Networks: []swarm.NetworkAttachmentConfig{
				{Target: spec.NetworkName},
			},
			RestartPolicy: &swarm.RestartPolicy{
				Condition: swarm.RestartPolicyConditionAny,
			},
		},
		Mode: swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{
				Replicas: &spec.Replicas,
			},
		},
		EndpointSpec: &swarm.EndpointSpec{
			Ports: portConfigs,
		},
	}

	// Check if service already exists
	existingSvc, _, err := cli.ServiceInspectWithRaw(ctx, spec.Name, types.ServiceInspectOptions{})
	if err == nil {
		// Update existing service
		resp, err := cli.ServiceUpdate(ctx, existingSvc.ID, existingSvc.Version, serviceSpec, types.ServiceUpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("service update failed: %w", err)
		}
		if len(resp.Warnings) > 0 {
			fmt.Printf("[swarm] service update warnings: %v\n", resp.Warnings)
		}
		return existingSvc.ID, nil
	}

	// Create new service
	resp, err := cli.ServiceCreate(ctx, serviceSpec, types.ServiceCreateOptions{})
	if err != nil {
		return "", fmt.Errorf("service create failed: %w", err)
	}
	return resp.ID, nil
}

// ScaleSwarmService adjusts the replica count for a given Swarm service.
func ScaleSwarmService(ctx context.Context, serviceNameOrID string, targetReplicas uint64) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	svc, _, err := cli.ServiceInspectWithRaw(ctx, serviceNameOrID, types.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect service %s: %w", serviceNameOrID, err)
	}

	spec := svc.Spec
	if spec.Mode.Replicated == nil {
		spec.Mode.Replicated = &swarm.ReplicatedService{}
	}
	spec.Mode.Replicated.Replicas = &targetReplicas

	_, err = cli.ServiceUpdate(ctx, svc.ID, svc.Version, spec, types.ServiceUpdateOptions{})
	if err != nil {
		return fmt.Errorf("scale service %s to %d replicas: %w", serviceNameOrID, targetReplicas, err)
	}
	return nil
}

// RemoveSwarmService removes a Swarm service by name or ID.
func RemoveSwarmService(ctx context.Context, serviceNameOrID string) error {
	cli, err := GetClient()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	return cli.ServiceRemove(ctx, serviceNameOrID)
}

// ListSwarmNodes returns all nodes currently in the Swarm.
func ListSwarmNodes(ctx context.Context) ([]SwarmNodeSummary, error) {
	cli, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	nodes, err := cli.NodeList(ctx, types.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("node list: %w", err)
	}

	var out []SwarmNodeSummary
	for _, n := range nodes {
		isLeader := false
		if n.ManagerStatus != nil && n.ManagerStatus.Leader {
			isLeader = true
		}
		out = append(out, SwarmNodeSummary{
			ID:            n.ID,
			Hostname:      n.Description.Hostname,
			Role:          string(n.Spec.Role),
			Status:        string(n.Status.State),
			Availability:  string(n.Spec.Availability),
			ManagerLeader: isLeader,
		})
	}
	return out, nil
}

// ListSwarmServices returns all services running in the Swarm.
func ListSwarmServices(ctx context.Context) ([]SwarmServiceSummary, error) {
	cli, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	services, err := cli.ServiceList(ctx, types.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("service list: %w", err)
	}

	var out []SwarmServiceSummary
	for _, s := range services {
		replicas := uint64(0)
		if s.Spec.Mode.Replicated != nil && s.Spec.Mode.Replicated.Replicas != nil {
			replicas = *s.Spec.Mode.Replicated.Replicas
		}

		var ports []string
		if s.Endpoint.Ports != nil {
			for _, p := range s.Endpoint.Ports {
				ports = append(ports, fmt.Sprintf("%d:%d/%s", p.PublishedPort, p.TargetPort, p.Protocol))
			}
		}

		out = append(out, SwarmServiceSummary{
			ID:        s.ID,
			Name:      s.Spec.Name,
			Image:     s.Spec.TaskTemplate.ContainerSpec.Image,
			Replicas:  replicas,
			Ports:     strings.Join(ports, ", "),
			CreatedAt: s.CreatedAt.String(),
		})
	}
	return out, nil
}

// SwarmTaskSummary represents a running task replica assigned to a specific node.
type SwarmTaskSummary struct {
	ID           string `json:"id"`
	ServiceID    string `json:"service_id"`
	ServiceName  string `json:"service_name"`
	NodeID       string `json:"node_id"`
	Slot         int    `json:"slot"`
	State        string `json:"state"`
	DesiredState string `json:"desired_state"`
	Image        string `json:"image"`
	ContainerID  string `json:"container_id"`
}

// ListSwarmTasks returns all active task replicas across the cluster.
func ListSwarmTasks(ctx context.Context) ([]SwarmTaskSummary, error) {
	cli, err := GetClient()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	tasks, err := cli.TaskList(ctx, types.TaskListOptions{})
	if err != nil {
		return nil, fmt.Errorf("task list: %w", err)
	}

	// Fetch service map for name resolution
	services, _ := cli.ServiceList(ctx, types.ServiceListOptions{})
	svcMap := make(map[string]string)
	for _, s := range services {
		svcMap[s.ID] = s.Spec.Name
	}

	var out []SwarmTaskSummary
	for _, t := range tasks {
		// Only active/running or desired-running tasks
		if t.DesiredState != swarm.TaskStateRunning && t.Status.State != swarm.TaskStateRunning {
			continue
		}
		cID := ""
		if t.Status.ContainerStatus != nil {
			cID = t.Status.ContainerStatus.ContainerID
		}
		img := ""
		if t.Spec.ContainerSpec != nil {
			img = t.Spec.ContainerSpec.Image
		}
		sName := svcMap[t.ServiceID]
		if sName == "" {
			sName = t.ServiceID
		}

		out = append(out, SwarmTaskSummary{
			ID:           t.ID,
			ServiceID:    t.ServiceID,
			ServiceName:  sName,
			NodeID:       t.NodeID,
			Slot:         t.Slot,
			State:        string(t.Status.State),
			DesiredState: string(t.DesiredState),
			Image:        img,
			ContainerID:  cID,
		})
	}
	return out, nil
}
