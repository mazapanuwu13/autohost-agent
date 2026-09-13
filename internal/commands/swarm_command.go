package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"autohost-agent/internal/infra/docker"
)

// SwarmInitCommand initializes a Swarm manager and returns the join tokens.
type SwarmInitCommand struct{}

type swarmInitParams struct {
	AdvertiseVPNIP string `json:"advertise_vpn_ip"`
	OverlayMTU     int    `json:"overlay_mtu"`
}

func (c *SwarmInitCommand) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *SwarmInitCommand) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	raw, _ := payload["params"].(string)
	var p swarmInitParams
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}

	tokens, err := docker.SwarmInit(ctx, p.AdvertiseVPNIP, p.OverlayMTU)
	if err != nil {
		return "", fmt.Errorf("swarm.init: %w", err)
	}

	outBytes, err := json.Marshal(tokens)
	if err != nil {
		return "", fmt.Errorf("swarm.init marshal: %w", err)
	}
	return string(outBytes), nil
}

// SwarmJoinCommand joins a worker or secondary manager node to the cluster.
type SwarmJoinCommand struct{}

type swarmJoinParams struct {
	ManagerVPNIP   string `json:"manager_vpn_ip"`
	JoinToken      string `json:"join_token"`
	AdvertiseVPNIP string `json:"advertise_vpn_ip"`
}

func (c *SwarmJoinCommand) Execute(ctx context.Context, payload map[string]any) error {
	raw, ok := payload["params"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("swarm.join: missing params in payload")
	}

	var p swarmJoinParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("swarm.join decode params: %w", err)
	}

	if p.ManagerVPNIP == "" || p.JoinToken == "" {
		return fmt.Errorf("swarm.join: manager_vpn_ip and join_token are required")
	}

	if err := docker.SwarmJoin(ctx, p.ManagerVPNIP, p.JoinToken, p.AdvertiseVPNIP); err != nil {
		return fmt.Errorf("swarm.join: %w", err)
	}
	return nil
}

// SwarmLeaveCommand leaves the Swarm cluster.
type SwarmLeaveCommand struct{}

type swarmLeaveParams struct {
	Force bool `json:"force"`
}

func (c *SwarmLeaveCommand) Execute(ctx context.Context, payload map[string]any) error {
	force := true
	if raw, ok := payload["params"].(string); ok && raw != "" {
		var p swarmLeaveParams
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			force = p.Force
		}
	}
	return docker.SwarmLeave(ctx, force)
}

// SwarmServiceDeployCommand deploys or updates a stateless service in the Swarm.
type SwarmServiceDeployCommand struct{}

func (c *SwarmServiceDeployCommand) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *SwarmServiceDeployCommand) ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error) {
	raw, ok := payload["params"].(string)
	if !ok || raw == "" {
		return "", fmt.Errorf("swarm.service.deploy: missing params in payload")
	}

	var spec docker.DeployServiceSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return "", fmt.Errorf("swarm.service.deploy decode params: %w", err)
	}

	if spec.Name == "" || spec.Image == "" {
		return "", fmt.Errorf("swarm.service.deploy: name and image are required")
	}

	serviceID, err := docker.DeploySwarmService(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("swarm.service.deploy: %w", err)
	}

	resp, _ := json.Marshal(map[string]string{"service_id": serviceID})
	return string(resp), nil
}

// SwarmServiceScaleCommand scales an existing service in the Swarm.
type SwarmServiceScaleCommand struct{}

type swarmScaleParams struct {
	ServiceName string `json:"service_name"`
	Replicas    uint64 `json:"replicas"`
}

func (c *SwarmServiceScaleCommand) Execute(ctx context.Context, payload map[string]any) error {
	raw, ok := payload["params"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("swarm.service.scale: missing params in payload")
	}

	var p swarmScaleParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("swarm.service.scale decode params: %w", err)
	}

	if p.ServiceName == "" {
		return fmt.Errorf("swarm.service.scale: service_name is required")
	}

	return docker.ScaleSwarmService(ctx, p.ServiceName, p.Replicas)
}

// SwarmServiceRemoveCommand removes a service from the Swarm.
type SwarmServiceRemoveCommand struct{}

type swarmRemoveParams struct {
	ServiceName string `json:"service_name"`
}

func (c *SwarmServiceRemoveCommand) Execute(ctx context.Context, payload map[string]any) error {
	raw, ok := payload["params"].(string)
	if !ok || raw == "" {
		return fmt.Errorf("swarm.service.remove: missing params in payload")
	}

	var p swarmRemoveParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return fmt.Errorf("swarm.service.remove decode params: %w", err)
	}

	if p.ServiceName == "" {
		return fmt.Errorf("swarm.service.remove: service_name is required")
	}

	return docker.RemoveSwarmService(ctx, p.ServiceName)
}

// SwarmInfoCommand reports the Swarm cluster nodes and services.
type SwarmInfoCommand struct{}

func (c *SwarmInfoCommand) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *SwarmInfoCommand) ExecuteWithOutput(ctx context.Context, _ map[string]any) (string, error) {
	nodes, err := docker.ListSwarmNodes(ctx)
	if err != nil {
		return "", fmt.Errorf("swarm.info nodes: %w", err)
	}

	services, err := docker.ListSwarmServices(ctx)
	if err != nil {
		return "", fmt.Errorf("swarm.info services: %w", err)
	}

	tasks, _ := docker.ListSwarmTasks(ctx)

	out, err := json.Marshal(map[string]any{
		"nodes":    nodes,
		"services": services,
		"tasks":    tasks,
	})
	if err != nil {
		return "", fmt.Errorf("swarm.info marshal: %w", err)
	}
	return string(out), nil
}

// SwarmTasksCommand returns the running task replicas and their assigned nodes.
type SwarmTasksCommand struct{}

func (c *SwarmTasksCommand) Execute(ctx context.Context, payload map[string]any) error {
	_, err := c.ExecuteWithOutput(ctx, payload)
	return err
}

func (c *SwarmTasksCommand) ExecuteWithOutput(ctx context.Context, _ map[string]any) (string, error) {
	tasks, err := docker.ListSwarmTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("swarm.tasks.list: %w", err)
	}
	out, err := json.Marshal(tasks)
	if err != nil {
		return "", fmt.Errorf("swarm.tasks marshal: %w", err)
	}
	return string(out), nil
}
