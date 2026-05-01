package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/configengine"
	"github.com/shlande/singbox-operator/internal/credmanager"
)

const (
	protoVless  = "vless"
	protoTrojan = "trojan"
	protoSocks5 = "socks5"
	protoHTTP   = "http"
)

func TestBuildClientConfig_TwoOutboundNodes(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound1 := makeOutboundNode("node-b1", "us")
	outbound2 := makeOutboundNode("node-b2", "us")

	user := makeProxyUser("user-alice", "vless", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{
			"node-b1": outbound1,
			"node-b2": outbound2,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 4 {
		t.Errorf("expected 4 outbounds (2 proxy + selector + direct), got %d", len(result))
	}

	var selectorOutbounds []string
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "selector" {
			arr, _ := m["outbounds"].([]string)
			selectorOutbounds = arr
		}
	}

	if len(selectorOutbounds) != 2 {
		t.Errorf("selector.outbounds should contain 2 tags, got %v", selectorOutbounds)
	}
}

func TestBuildClientConfig_DerivedUUID(t *testing.T) {
	const baseUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const outboundName = "node-b"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode(outboundName, "us")

	user := makeProxyUser("user-alice", "vless", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: baseUUID},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{outboundName: outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedUUID := configengine.DeriveUUID(baseUUID, outboundName)

	var foundUUID string
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == protoVless {
			foundUUID, _ = m["uuid"].(string)
		}
	}

	if foundUUID != expectedUUID {
		t.Errorf("expected derived UUID %q, got %q", expectedUUID, foundUUID)
	}
}

func TestBuildClientConfig_TrojanPassword(t *testing.T) {
	const basePassword = "secret"
	const outboundName = "node-c"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "trojan", Port: 10444},
	})
	inbound.Status.EntryEndpoints = []string{"trojan:1.2.3.4:10444"}

	outbound := makeOutboundNode(outboundName, "us")

	user := makeProxyUser("user-bob", "trojan", "secret-bob")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{Password: basePassword},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{outboundName: outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPassword := configengine.DerivePassword(basePassword, outboundName)

	var foundPassword string
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == protoTrojan {
			foundPassword, _ = m["password"].(string)
		}
	}

	if foundPassword != expectedPassword {
		t.Errorf("expected derived password %q, got %q", expectedPassword, foundPassword)
	}
}

func TestBuildClientConfig_EmptyEntryEndpoints(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})

	outbound := makeOutboundNode("node-b", "us")
	user := makeProxyUser("user-alice", "vless", "secret-alice")

	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{"node-b": outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 outbounds (selector+direct) with empty EntryEndpoints, got %d", len(result))
	}

	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		tp, _ := m["type"].(string)
		if tp == protoVless || tp == protoTrojan {
			t.Errorf("unexpected proxy outbound with type %q when EntryEndpoints is empty", tp)
		}
	}
}

func TestBuildClientConfig_ExplicitRoutes(t *testing.T) {
	// outbound-x and outbound-y are both in region "us" (same as inbound node-a).
	// A ProxyRoute exists for outbound-x only.
	// Expected: BOTH nodes appear because the union of same-region + explicit routes is used,
	// mirroring server-side configengine.buildRouteInbounds behaviour.
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outboundX := makeOutboundNode("outbound-x", "us")
	outboundY := makeOutboundNode("outbound-y", "us")

	route := makeProxyRoute("route-1", "default", "node-a", "outbound-x")
	user := makeProxyUser("user-alice", "vless", "secret-alice")

	input := ClientConfigInput{
		User:         user,
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{
			"node-a": {route},
		},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{
			"outbound-x": outboundX,
			"outbound-y": outboundY,
		},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 4 {
		t.Errorf("expected 4 outbounds (2 proxy+selector+direct), got %d", len(result))
	}

	tags := make(map[string]bool)
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); tag != "" {
			tags[tag] = true
		}
	}
	if !tags["node-a-outbound-x"] {
		t.Error("expected node-a-outbound-x in result")
	}
	if !tags["node-a-outbound-y"] {
		t.Error("expected node-a-outbound-y in result (same region as inbound)")
	}
}

func TestMergeOutbounds_ReplaceOutbounds(t *testing.T) {
	tmpl := []byte(`{"outbounds": [{"type":"direct","tag":"old"}]}`)
	newOutbounds := []interface{}{
		map[string]interface{}{"type": "direct", "tag": "new"},
	}

	result, err := MergeOutbounds(tmpl, newOutbounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	obs, _ := m["outbounds"].([]interface{})
	if len(obs) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(obs))
	}

	first, _ := obs[0].(map[string]interface{})
	if first["tag"] != "new" {
		t.Errorf("expected outbound tag 'new', got %v", first["tag"])
	}
}

func TestMergeOutbounds_PreserveInbounds(t *testing.T) {
	newOutbounds := []interface{}{
		map[string]interface{}{"type": "direct", "tag": "direct"},
	}

	result, err := MergeOutbounds(DefaultTemplate, newOutbounds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	inbounds, _ := m["inbounds"].([]interface{})
	if len(inbounds) == 0 {
		t.Fatal("expected inbounds to be preserved, got empty array")
	}

	tags := make(map[string]bool)
	for _, ib := range inbounds {
		im, _ := ib.(map[string]interface{})
		tag, _ := im["tag"].(string)
		tags[tag] = true
	}

	if !tags["socks-in"] {
		t.Errorf("expected socks-in inbound to be preserved, got %v", tags)
	}
	if !tags["http-in"] {
		t.Errorf("expected http-in inbound to be preserved, got %v", tags)
	}
}

func TestMergeOutbounds_InvalidTemplate(t *testing.T) {
	_, err := MergeOutbounds([]byte("not json"), []interface{}{})
	if err == nil {
		t.Error("expected error for invalid template JSON, got nil")
	}
}

func TestHandler_InvalidUUID(t *testing.T) {
	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      newFakeClient(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/default/not-a-uuid", nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected HTTP 400, got %d", w.Code)
	}
}

func TestHandler_MissingPath(t *testing.T) {
	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      newFakeClient(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/", nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected HTTP 404, got %d", w.Code)
	}
}

func TestHandler_UUIDNotFound(t *testing.T) {
	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      newFakeClient(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/default/f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected HTTP 404, got %d", w.Code)
	}
}

func TestHandler_Success(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")

	user := makeProxyUser("user-alice", "vless", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	fakeClient := newFakeClient(secret, user, inbound, outbound)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d; body: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	obs, _ := parsed["outbounds"].([]interface{})
	if len(obs) == 0 {
		t.Error("expected non-empty outbounds array in response")
	}
}

func makeInboundNode(name, region, address string, protocols []proxyv1alpha1.ProtocolConfig) *proxyv1alpha1.ProxyNode {
	return &proxyv1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: proxyv1alpha1.ProxyNodeSpec{
			NodeRef:            name,
			Address:            address,
			Region:             region,
			Roles:              []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleInbound},
			SupportedProtocols: protocols,
			RelayPort:          10808,
		},
	}
}

func makeOutboundNode(name, region string) *proxyv1alpha1.ProxyNode {
	return &proxyv1alpha1.ProxyNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: proxyv1alpha1.ProxyNodeSpec{
			NodeRef:   name,
			Address:   "10.0.0.1",
			Region:    region,
			Roles:     []proxyv1alpha1.ProxyRole{proxyv1alpha1.ProxyRoleOutbound},
			RelayPort: 10808,
		},
	}
}

func makeProxyUser(name, protocol, secretName string) *proxyv1alpha1.ProxyUser {
	return &proxyv1alpha1.ProxyUser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: proxyv1alpha1.ProxyUserSpec{
			Protocol: protocol,
			AuthSecret: corev1.SecretReference{
				Name:      secretName,
				Namespace: "default",
			},
		},
	}
}

func makeUserSecret(namespace, name, uuid, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"uuid":     []byte(uuid),
			"password": []byte(password),
		},
	}
}

func makeProxyRoute(name, namespace, inbound, outbound string) *proxyv1alpha1.ProxyRoute {
	return &proxyv1alpha1.ProxyRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: proxyv1alpha1.ProxyRouteSpec{
			InboundNode:  inbound,
			OutboundNode: outbound,
		},
	}
}

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = proxyv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestBuildClientConfig_Socks5Password(t *testing.T) {
	const basePassword = "mysecret"
	const outboundName = "node-d"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "socks5", Port: 10808},
	})
	inbound.Status.EntryEndpoints = []string{"socks5:1.2.3.4:10808"}

	outbound := makeOutboundNode(outboundName, "us")

	user := makeProxyUser("user-charlie", "socks5", "secret-charlie")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{Password: basePassword, Username: "charlie"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{outboundName: outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPassword := configengine.DerivePassword(basePassword, outboundName)

	var foundPassword string
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "socks" {
			foundPassword, _ = m["password"].(string)
		}
	}

	if foundPassword != expectedPassword {
		t.Errorf("expected derived socks5 password %q, got %q", expectedPassword, foundPassword)
	}
}

func TestBuildClientConfig_HTTPPassword(t *testing.T) {
	const basePassword = "httppass"
	const outboundName = "node-e"

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "http", Port: 10080},
	})
	inbound.Status.EntryEndpoints = []string{"http:1.2.3.4:10080"}

	outbound := makeOutboundNode(outboundName, "us")

	user := makeProxyUser("user-dave", "http", "secret-dave")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{Password: basePassword, Username: "dave"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{outboundName: outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPassword := configengine.DerivePassword(basePassword, outboundName)

	var foundPassword string
	for _, ob := range result {
		m, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == protoHTTP {
			foundPassword, _ = m["password"].(string)
		}
	}

	if foundPassword != expectedPassword {
		t.Errorf("expected derived http password %q, got %q", expectedPassword, foundPassword)
	}
}

func TestHandler_TemplateRef_InvalidFormat(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")
	user := makeProxyUser("user-alice", "vless", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	fakeClient := newFakeClient(secret, user, inbound, outbound)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "invalid-no-slash",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 (falls back to default), got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandler_TemplateRef_MissingConfigMap(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")
	user := makeProxyUser("user-alice", "vless", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	fakeClient := newFakeClient(secret, user, inbound, outbound)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "default/nonexistent-configmap",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 (falls back to default on missing CM), got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandler_TemplateRef_WithConfigMap(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")
	user := makeProxyUser("user-alice", "vless", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-template",
			Namespace: namespace,
		},
		Data: map[string]string{
			"config.json": `{"inbounds":[],"outbounds":[],"route":{"final":"direct"}}`,
		},
	}

	fakeClient := newFakeClient(secret, user, inbound, outbound, cm)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: namespace + "/my-template",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 with custom template, got %d; body: %s", w.Code, w.Body.String())
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestBuildClientConfig_UnsupportedProtocol(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "trojan", Port: 10444},
	})
	inbound.Status.EntryEndpoints = []string{"trojan:1.2.3.4:10444"}

	outbound := makeOutboundNode("node-b", "us")

	user := makeProxyUser("user-alice", "vless", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{"node-b": outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 outbounds (selector+direct) when protocol not supported, got %d", len(result))
	}
}

func TestBuildClientConfig_BadEndpointFormat(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{
		"noport",
		"vless:1.2.3.4:notanumber",
		"other:1.2.3.4:9999",
	}

	outbound := makeOutboundNode("node-b", "us")

	user := makeProxyUser("user-alice", "vless", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{"node-b": outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 outbounds (selector+direct) with bad endpoints, got %d", len(result))
	}
}

func TestBuildClientConfig_NullInboundInResolve(t *testing.T) {
	outbound := makeOutboundNode("node-b", "us")
	user := makeProxyUser("user-alice", "vless", "secret-alice")
	input := ClientConfigInput{
		User:            user,
		UserCred:        credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes:    []*proxyv1alpha1.ProxyNode{},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{"node-b": outbound},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 outbounds (selector+direct) with no inbounds, got %d", len(result))
	}
}

func TestHandler_TemplateRef_ConfigMapMissingKey(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret := makeUserSecret(namespace, "test-secret", testUUID, "pw")
	user := makeProxyUser("user-alice", "vless", "test-secret")
	user.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "empty-template",
			Namespace: namespace,
		},
		Data: map[string]string{},
	}

	fakeClient := newFakeClient(secret, user, inbound, outbound, cm)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: namespace + "/empty-template",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 (falls back to default on missing key), got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestWriteInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	writeInternalError(w)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected HTTP 500, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if parsed["error"] == "" {
		t.Error("expected non-empty error field in response")
	}
}

func TestBuildClientConfig_RouteWithMissingOutbound(t *testing.T) {
	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	route := makeProxyRoute("route-1", "default", "node-a", "nonexistent-outbound")
	user := makeProxyUser("user-alice", "vless", "secret-alice")

	input := ClientConfigInput{
		User:         user,
		UserCred:     credmanager.UserCredential{UUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		InboundNodes: []*proxyv1alpha1.ProxyNode{inbound},
		RoutesByInbound: map[string][]*proxyv1alpha1.ProxyRoute{
			"node-a": {route},
		},
		OutboundsByName: map[string]*proxyv1alpha1.ProxyNode{},
	}

	result, err := BuildClientConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 outbounds (selector+direct) when route outbound is missing, got %d", len(result))
	}
}

func TestHandler_MultipleUsersMatchUUID(t *testing.T) {
	const testUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	const namespace = "default"

	secret1 := makeUserSecret(namespace, "secret-1", testUUID, "pw1")
	secret2 := makeUserSecret(namespace, "secret-2", testUUID, "pw2")
	secret2.Name = "secret-2"

	user1 := makeProxyUser("user-1", "vless", "secret-1")
	user1.Namespace = namespace

	user2 := makeProxyUser("user-2", "vless", "secret-2")
	user2.Namespace = namespace

	inbound := makeInboundNode("node-a", "us", "1.2.3.4", []proxyv1alpha1.ProtocolConfig{
		{Protocol: "vless", Port: 10443},
	})
	inbound.Namespace = namespace
	inbound.Status.EntryEndpoints = []string{"vless:1.2.3.4:10443"}

	outbound := makeOutboundNode("node-b", "us")
	outbound.Namespace = namespace

	fakeClient := newFakeClient(secret1, secret2, user1, user2, inbound, outbound)

	srv := &Server{
		BindAddress: ":0",
		TemplateRef: "",
		Client:      fakeClient,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/client-config/"+namespace+"/"+testUUID, nil)
	w := httptest.NewRecorder()
	srv.handleClientConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 for first matching user, got %d; body: %s", w.Code, w.Body.String())
	}
}
