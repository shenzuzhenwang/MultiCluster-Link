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

// VpcDnsForwardSpec defines the desired state of VpcDnsForward
type VpcDnsForwardSpec struct {
	Vpc string `json:"vpc,omitempty"`
}

// VpcDnsForwardStatus defines the observed state of VpcDnsForward
type VpcDnsForwardStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VpcDnsForward is the Schema for the vpcdnsforwards API
type VpcDnsForward struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VpcDnsForwardSpec   `json:"spec,omitempty"`
	Status VpcDnsForwardStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VpcDnsForwardList contains a list of VpcDnsForward
type VpcDnsForwardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VpcDnsForward `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VpcDnsForward{}, &VpcDnsForwardList{})
}
