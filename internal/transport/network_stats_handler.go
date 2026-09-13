package transport

import (
	"context"
	"encoding/json"
	"log"

	"autohost-agent/internal/api"
	pb "autohost-agent/internal/grpc/nodepb"
	"autohost-agent/pkg/sysinfo"
)

// handleRequestNetworkStats is invoked when the server requests an on-demand network stats snapshot.
func (c *GRPCClient) handleRequestNetworkStats(ctx context.Context, req *pb.RequestNetworkStatsPayload, results chan<- *pb.NodeMessage) {
	requestID := req.GetRequestId()
	log.Printf("🌐 gRPC: collecting network stats on-demand for request %s", requestID)

	ifaces, err := sysinfo.GetNetworkStats()
	if err != nil {
		log.Printf("⚠️  network stats: %v", err)
		sendNetworkStatsError(requestID, err.Error(), results)
		return
	}
	ports, err := sysinfo.GetListeningPorts()
	if err != nil {
		log.Printf("⚠️  listening ports: %v", err)
		sendNetworkStatsError(requestID, err.Error(), results)
		return
	}
	peers, _ := sysinfo.GetVPNPeers()
	conns, _ := sysinfo.GetActiveConnections()
	reqs, rps, _ := sysinfo.GetRecentHTTPRequests()

	payload := &api.NetworkStatsPayload{
		Interfaces:  ifaces,
		Ports:       ports,
		Peers:       peers,
		Connections: conns,
		Requests:    reqs,
		RPS:         rps,
	}

	bs, err := json.Marshal(payload)
	if err != nil {
		log.Printf("⚠️  marshal network stats: %v", err)
		sendNetworkStatsError(requestID, err.Error(), results)
		return
	}

	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_NetworkStats{
			NetworkStats: &pb.NetworkStatsPayload{
				RequestId: requestID,
				StatsJson: string(bs),
			},
		},
	}

	select {
	case results <- msg:
		log.Printf("✅ gRPC: sent on-demand network stats for request %s", requestID)
	case <-ctx.Done():
		log.Printf("⚠️  network stats %s: context cancelled before sending", requestID)
	default:
		log.Printf("⚠️  network stats %s: results buffer full, dropping", requestID)
	}
}

// sendNetworkStatsError sends a NetworkStatsPayload with only the error field set.
func sendNetworkStatsError(requestID, errMsg string, results chan<- *pb.NodeMessage) {
	msg := &pb.NodeMessage{
		Payload: &pb.NodeMessage_NetworkStats{
			NetworkStats: &pb.NetworkStatsPayload{
				RequestId: requestID,
				Error:     errMsg,
			},
		},
	}
	select {
	case results <- msg:
	default:
	}
}
