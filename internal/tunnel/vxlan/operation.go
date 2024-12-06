package vxlan

import (
	"fmt"
	v1 "kubeovn-multivpc/api/v1"
	"kubeovn-multivpc/internal/tunnel"
)

const (
	DefaultVid  string = "100"
	DefaultPort string = "4789"
)

type VxlanOperation struct {
	tunnel *v1.VpcNatTunnel
}

func NewVxlanOp(tunnel *v1.VpcNatTunnel) tunnel.TunnelOperation {
	return &VxlanOperation{
		tunnel: tunnel,
	}
}

func (v *VxlanOperation) CreateCmd() string {
	tunnel := v.tunnel
	vid, port := getVidAndPort(tunnel)

	createCmd := fmt.Sprintf("ip link add %s type vxlan id %s dev net1 dstport %s remote %s local %s", tunnel.Name, vid, port, tunnel.Status.RemoteIP, tunnel.Status.InternalIP)
	setUpCmd := fmt.Sprintf("ip link set %s up", tunnel.Name)
	addrCmd := fmt.Sprintf("ip addr add %s dev %s", tunnel.Status.InterfaceAddr, tunnel.Name)
	return createCmd + ";" + setUpCmd + ";" + addrCmd
}

func (v *VxlanOperation) DeleteCmd() string {
	delCmd := fmt.Sprintf("ip link del %s", v.tunnel.Name)
	return delCmd
}

func getVidAndPort(t *v1.VpcNatTunnel) (string, string) {
	retVid := DefaultVid
	retPort := DefaultPort
	if vid, ok := t.Labels["vid"]; ok {
		retVid = vid
	}
	if port, ok := t.Labels["vx-port"]; ok {
		retPort = port
	}
	return retVid, retPort
}
