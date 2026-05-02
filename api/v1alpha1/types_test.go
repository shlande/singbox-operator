/*
Copyright 2026.

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

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSingBoxNodeDeepCopy(t *testing.T) {
	original := &SingBoxNode{
		Spec: SingBoxNodeSpec{
			NodeRef: "node-1",
			Address: "1.2.3.4",
			Region:  "us-west",
			Roles:   []ProxyRole{ProxyRoleInbound},
			SupportedProtocols: []ProtocolConfig{
				{Protocol: "vless", Port: 30443},
			},
			RelayPort: 31962,
		},
		Status: SingBoxNodeStatus{
			Phase:      "Running",
			ConfigHash: "abc123",
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
		},
	}
	copy := original.DeepCopy()
	if copy == original {
		t.Fatal("DeepCopy returned same pointer")
	}
	if copy.Spec.NodeRef != original.Spec.NodeRef {
		t.Errorf("NodeRef mismatch: %s != %s", copy.Spec.NodeRef, original.Spec.NodeRef)
	}
	if len(copy.Spec.SupportedProtocols) != len(original.Spec.SupportedProtocols) {
		t.Error("SupportedProtocols length mismatch after DeepCopy")
	}
	copy.Spec.Roles = append(copy.Spec.Roles, ProxyRoleOutbound)
	if len(original.Spec.Roles) != 1 {
		t.Error("Original Roles mutated after copy modification")
	}
}

func TestUserDeepCopy(t *testing.T) {
	original := &User{
		Spec: UserSpec{
			Protocol: "vless",
		},
		Status: UserStatus{
			ActiveNodeCount: 2,
			ActiveNodes:     []string{"node-a", "node-b"},
		},
	}
	copy := original.DeepCopy()
	if copy == original {
		t.Fatal("DeepCopy returned same pointer")
	}
	copy.Status.ActiveNodes = append(copy.Status.ActiveNodes, "node-c")
	if len(original.Status.ActiveNodes) != 2 {
		t.Error("Original ActiveNodes mutated after copy modification")
	}
}

func TestCustomRouteDeepCopy(t *testing.T) {
	original := &CustomRoute{
		Spec: CustomRouteSpec{
			InboundNode:  "inbound-a",
			OutboundNode: "outbound-b",
		},
	}
	copy := original.DeepCopy()
	if copy == original {
		t.Fatal("DeepCopy returned same pointer")
	}
	if copy.Spec.InboundNode != original.Spec.InboundNode {
		t.Errorf("InboundNode mismatch: %s != %s", copy.Spec.InboundNode, original.Spec.InboundNode)
	}
}

func TestSingBoxNodeStatusConditionsInit(t *testing.T) {
	node := &SingBoxNode{}
	if node.Status.Conditions != nil {
		t.Error("Expected nil Conditions on empty SingBoxNode")
	}
	node.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Initializing",
			Message:            "Node is being initialized",
			ObservedGeneration: 1,
		},
	}
	if len(node.Status.Conditions) != 1 {
		t.Error("Expected 1 condition")
	}
}
