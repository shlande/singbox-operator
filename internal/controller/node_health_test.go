package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

func nodeWithReadyStatus(status corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: status},
			},
		},
	}
}

func nodeWithAnnotations(annotations map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-node",
			Annotations: annotations,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func TestIsNodeReady(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "Ready=True returns true",
			node: nodeWithReadyStatus(corev1.ConditionTrue),
			want: true,
		},
		{
			name: "Ready=False returns false",
			node: nodeWithReadyStatus(corev1.ConditionFalse),
			want: false,
		},
		{
			name: "no Ready condition returns false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				Status:     corev1.NodeStatus{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNodeReady(tt.node)
			if got != tt.want {
				t.Errorf("isNodeReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNodeOfflineAnnotated(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "offline annotation set to true returns true",
			node: nodeWithAnnotations(map[string]string{proxyv1alpha1.OfflineAnnotation: "true"}),
			want: true,
		},
		{
			name: "no offline annotation returns false",
			node: nodeWithAnnotations(nil),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNodeOfflineAnnotated(tt.node)
			if got != tt.want {
				t.Errorf("isNodeOfflineAnnotated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNodeAvailable(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "ready and not offline annotated returns true",
			node: nodeWithReadyStatus(corev1.ConditionTrue),
			want: true,
		},
		{
			name: "not ready returns false",
			node: nodeWithReadyStatus(corev1.ConditionFalse),
			want: false,
		},
		{
			name: "ready but offline annotated returns false",
			node: nodeWithAnnotations(map[string]string{proxyv1alpha1.OfflineAnnotation: "true"}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNodeAvailable(tt.node)
			if got != tt.want {
				t.Errorf("isNodeAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeReadyPredicate(t *testing.T) {
	tests := []struct {
		name   string
		oldObj any
		newObj any
		want   bool
	}{
		{
			name:   "Ready condition changed from True to False returns true",
			oldObj: nodeWithReadyStatus(corev1.ConditionTrue),
			newObj: nodeWithReadyStatus(corev1.ConditionFalse),
			want:   true,
		},
		{
			name:   "Ready condition unchanged (both True) returns false",
			oldObj: nodeWithReadyStatus(corev1.ConditionTrue),
			newObj: nodeWithReadyStatus(corev1.ConditionTrue),
			want:   false,
		},
		{
			name:   "non-Node objects return false",
			oldObj: &appsv1.Deployment{},
			newObj: &appsv1.Deployment{},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NodeReadyConditionChangedPredicate{}
			evt := event.UpdateEvent{
				ObjectOld: tt.oldObj.(client.Object),
				ObjectNew: tt.newObj.(client.Object),
			}
			got := p.UpdateFunc(evt)
			if got != tt.want {
				t.Errorf("NodeReadyConditionChangedPredicate.UpdateFunc() = %v, want %v", got, tt.want)
			}
		})
	}
}
