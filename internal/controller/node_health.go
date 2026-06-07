package controller

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
)

// isNodeReady returns true if the Node has a Ready condition with Status=True.
func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// isNodeOfflineAnnotated returns true if the Node has the offline annotation set to "true".
func isNodeOfflineAnnotated(node *corev1.Node) bool {
	return node.Annotations[proxyv1alpha1.OfflineAnnotation] == "true"
}

// isNodeAvailable returns true if the Node is ready AND not manually taken offline.
func isNodeAvailable(node *corev1.Node) bool {
	return isNodeReady(node) && !isNodeOfflineAnnotated(node)
}

// NodeReadyConditionChangedPredicate filters Node update events to only trigger
// reconciliation when the NodeReady condition status actually changes.
type NodeReadyConditionChangedPredicate struct {
	predicate.Funcs
}

func (NodeReadyConditionChangedPredicate) UpdateFunc(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}
	oldNode, ok1 := e.ObjectOld.(*corev1.Node)
	newNode, ok2 := e.ObjectNew.(*corev1.Node)
	if !ok1 || !ok2 {
		return false
	}
	return getNodeReadyStatus(oldNode) != getNodeReadyStatus(newNode)
}

func getNodeReadyStatus(node *corev1.Node) corev1.ConditionStatus {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status
		}
	}
	return corev1.ConditionUnknown
}
