/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VpcNatTunnelSpec defines the desired state of VpcNatTunnel
type VpcNatTunnelSpec struct {
	RemoteCluster string `json:"remoteCluster"`
	RemoteVpc     string `json:"remoteVpc"`
	InterfaceAddr string `json:"interfaceAddr"`
	LocalVpc      string `json:"localVpc"`
	// +kubebuilder:default="gre"
	Type       string `json:"type,omitempty"`
	RemoteIP   string `json:"remoteIp,omitempty"`
	InternalIP string `json:"internalIp,omitempty"`
	LocalGw    string `json:"localGw,omitempty"`
}

// VpcNatTunnelStatus defines the observed state of VpcNatTunnel
type VpcNatTunnelStatus struct {
	Initialized   bool   `json:"initialized"`
	InternalIP    string `json:"internalIp"`
	RemoteIP      string `json:"remoteIp"`
	InterfaceAddr string `json:"interfaceAddr"`
	LocalGw       string `json:"localGw"`
	Type          string `json:"type"`

	GlobalnetCIDR       string   `json:"globalnetCIDR"`
	RemoteGlobalnetCIDR string   `json:"remoteGlobalnetCIDR"`
	OvnGwIP             string   `json:"ovnGwIP"`
	GlobalEgressIP      []string `json:"globalEgressIP"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VpcNatTunnel is the Schema for the vpcnattunnels API
type VpcNatTunnel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VpcNatTunnelSpec   `json:"spec,omitempty"`
	Status VpcNatTunnelStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VpcNatTunnelList contains a list of VpcNatTunnel
type VpcNatTunnelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VpcNatTunnel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VpcNatTunnel{}, &VpcNatTunnelList{})
}
