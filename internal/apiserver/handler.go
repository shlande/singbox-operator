package apiserver

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx)

	remaining := strings.TrimPrefix(r.URL.Path, "/api/v1/client-config/")
	parts := strings.SplitN(remaining, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}
	namespace, requestUUID := parts[0], parts[1]

	if !uuidRegex.MatchString(requestUUID) {
		http.Error(w, "invalid uuid", http.StatusBadRequest)
		return
	}

	var userList proxyv1alpha1.UserList
	if err := s.Client.List(ctx, &userList, client.InNamespace(namespace)); err != nil {
		logger.Error(err, "Failed to list Users", "namespace", namespace)
		writeInternalError(w)
		return
	}

	var matchedUser *proxyv1alpha1.User
	var matchedCred credmanager.UserCredential
	for i := range userList.Items {
		user := &userList.Items[i]
		cred, err := credmanager.GetUserCredential(ctx, s.Client, user)
		if err != nil {
			logger.Error(err, "Failed to get credential for user", "user", user.Name)
			continue
		}
		if !strings.EqualFold(cred.UUID, requestUUID) {
			continue
		}
		if matchedUser != nil {
			logger.Info("Multiple Users match UUID, using first match", "namespace", namespace, "uuid", requestUUID)
			break
		}
		matchedUser = user
		matchedCred = cred
	}

	if matchedUser == nil {
		http.NotFound(w, r)
		return
	}

	var nodeList proxyv1alpha1.SingBoxNodeList
	if err := s.Client.List(ctx, &nodeList, client.InNamespace(namespace)); err != nil {
		logger.Error(err, "Failed to list SingBoxNodes", "namespace", namespace)
		writeInternalError(w)
		return
	}

	var inboundNodes []*proxyv1alpha1.SingBoxNode
	outboundsByName := make(map[string]*proxyv1alpha1.SingBoxNode)
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if hasRole(node, proxyv1alpha1.ProxyRoleInbound) {
			inboundNodes = append(inboundNodes, node)
		}
		if hasRole(node, proxyv1alpha1.ProxyRoleOutbound) {
			outboundsByName[node.Name] = node
		}
	}

	var routeList proxyv1alpha1.CustomRouteList
	if err := s.Client.List(ctx, &routeList, client.InNamespace(namespace)); err != nil {
		logger.Error(err, "Failed to list CustomRoutes", "namespace", namespace)
		writeInternalError(w)
		return
	}

	routesByInbound := make(map[string][]*proxyv1alpha1.CustomRoute)
	for i := range routeList.Items {
		route := &routeList.Items[i]
		routesByInbound[route.Spec.InboundNode] = append(routesByInbound[route.Spec.InboundNode], route)
	}

	input := ClientConfigInput{
		User:            matchedUser,
		UserCred:        matchedCred,
		InboundNodes:    inboundNodes,
		RoutesByInbound: routesByInbound,
		OutboundsByName: outboundsByName,
	}

	outbounds, err := BuildClientConfig(input)
	if err != nil {
		logger.Error(err, "Failed to build client config", "user", matchedUser.Name)
		writeInternalError(w)
		return
	}

	templateBytes := DefaultTemplate
	if s.TemplateRef != "" {
		refParts := strings.SplitN(s.TemplateRef, "/", 2)
		if len(refParts) == 2 {
			var cm corev1.ConfigMap
			if err := s.Client.Get(ctx, types.NamespacedName{Namespace: refParts[0], Name: refParts[1]}, &cm); err != nil {
				logger.Error(err, "Failed to get template ConfigMap, using default", "templateRef", s.TemplateRef)
			} else if data, ok := cm.Data["config.json"]; ok {
				templateBytes = []byte(data)
			} else {
				logger.Info("Template ConfigMap missing config.json key, using default", "templateRef", s.TemplateRef)
			}
		} else {
			logger.Info("Invalid templateRef format, using default", "templateRef", s.TemplateRef)
		}
	}

	result, err := MergeOutbounds(templateBytes, outbounds)
	if err != nil {
		logger.Error(err, "Failed to merge outbounds into template")
		writeInternalError(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

func hasRole(node *proxyv1alpha1.SingBoxNode, role proxyv1alpha1.ProxyRole) bool {
	return slices.Contains(node.Spec.Roles, role)
}

func writeInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
}
