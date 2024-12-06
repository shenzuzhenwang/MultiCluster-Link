package factory

import (
	v1 "kubeovn-multivpc/api/v1"
	"kubeovn-multivpc/internal/tunnel"
	"kubeovn-multivpc/internal/tunnel/gre"
	"kubeovn-multivpc/internal/tunnel/vxlan"
)

type TunnelOperationFactory struct {
}

func NewTunnelOpFactory() *TunnelOperationFactory {
	return &TunnelOperationFactory{}
}

type TunnelType string

const (
	VXLAN = "vxlan"
	GRE   = "gre"
)

func (f *TunnelOperationFactory) CreateTunnelOperation(tunnel *v1.VpcNatTunnel) tunnel.TunnelOperation {
	switch tunnel.Spec.Type {
	case "vxlan":
		return vxlan.NewVxlanOp(tunnel)
	case "gre":
		return gre.NewGreOp(tunnel)
	default:
		return gre.NewGreOp(tunnel)
	}
}
