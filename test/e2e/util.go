//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	ibclient "github.com/infobloxopen/infoblox-go-client"
	"github.com/miekg/dns"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	operatorv1alpha1 "github.com/openshift/external-dns-operator/api/v1alpha1"
	operatorv1beta1 "github.com/openshift/external-dns-operator/api/v1beta1"
	"github.com/openshift/external-dns-operator/pkg/utils"
)

const (
	googleDNSServer = "8.8.8.8"
)

type providerTestHelper interface {
	ensureHostedZone(string) (string, []string, error)
	deleteHostedZone(string, string) error
	platform() string
	makeCredentialsSecret(namespace string) *corev1.Secret
	buildExternalDNS(name, zoneID, zoneDomain string, credsSecret *corev1.Secret) operatorv1beta1.ExternalDNS
	buildOpenShiftExternalDNS(name, zoneID, zoneDomain, routeName string, credsSecret *corev1.Secret) operatorv1beta1.ExternalDNS
	buildOpenShiftExternalDNSV1Alpha1(name, zoneID, zoneDomain, routeName string, credsSecret *corev1.Secret) operatorv1alpha1.ExternalDNS
	getDNSRecordValuesWithAssumeRole(assumeRoleARN, zoneId, recordName, recordType string) (map[string]struct{}, error)
}

func randomString(n int) string {
	var chars = []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	str := make([]rune, n)
	for i := range str {
		str[i] = chars[rand.Intn(len(chars))]
	}
	return string(str)
}

func getPlatformType(kubeClient client.Client) (string, error) {
	var infraConfig configv1.Infrastructure
	err := kubeClient.Get(context.Background(), types.NamespacedName{Name: "cluster"}, &infraConfig)
	if err != nil {
		return "", err
	}
	return string(infraConfig.Status.PlatformStatus.Type), nil
}

func defaultService(name, namespace string) *corev1.Service {
	return testService(name, namespace, corev1.ServiceTypeLoadBalancer)
}

func testRoute(name, namespace, host, svcName string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"external-dns.mydomain.org/publish": "yes",
			},
		},
		Spec: routev1.RouteSpec{
			Host: host,
			To: routev1.RouteTargetReference{
				Name: svcName,
			},
		},
	}
}

func testService(name, namespace string, svcType corev1.ServiceType) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"external-dns.mydomain.org/publish": "yes",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"name": "hello-openshift",
			},
			Type: svcType,
			Ports: []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     80,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 8080,
					},
				},
			},
		},
	}
}

func mustGetEnv(name string) string {
	val := os.Getenv(name)
	if val == "" {
		panic(fmt.Sprintf("environment variable %s must be set", name))
	}
	return val
}

func deploymentConditionMap(conditions ...appsv1.DeploymentCondition) map[string]string {
	conds := map[string]string{}
	for _, cond := range conditions {
		conds[string(cond.Type)] = string(cond.Status)
	}
	return conds
}

func waitForOperatorDeploymentStatusCondition(ctx context.Context, t *testing.T, cl client.Client, conditions ...appsv1.DeploymentCondition) error {
	t.Helper()
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 1*time.Minute, false, func(ctx context.Context) (bool, error) {
		dep := &appsv1.Deployment{}
		depNamespacedName := types.NamespacedName{
			Name:      "external-dns-operator",
			Namespace: "external-dns-operator",
		}
		if err := cl.Get(ctx, depNamespacedName, dep); err != nil {
			t.Logf("failed to get deployment %s: %v", depNamespacedName.Name, err)
			return false, nil
		}

		expected := deploymentConditionMap(conditions...)
		current := deploymentConditionMap(dep.Status.Conditions...)
		return conditionsMatchExpected(expected, current), nil
	})
}

func conditionsMatchExpected(expected, actual map[string]string) bool {
	filtered := map[string]string{}
	for k := range actual {
		if _, comparable := expected[k]; comparable {
			filtered[k] = actual[k]
		}
	}
	return reflect.DeepEqual(expected, filtered)
}

func defaultExternalDNS(name, zoneID, zoneDomain string) operatorv1beta1.ExternalDNS {
	return operatorv1beta1.ExternalDNS{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: operatorv1beta1.ExternalDNSSpec{
			Zones: []string{zoneID},
			Source: operatorv1beta1.ExternalDNSSource{
				ExternalDNSSourceUnion: operatorv1beta1.ExternalDNSSourceUnion{
					Type: operatorv1beta1.SourceTypeService,
					Service: &operatorv1beta1.ExternalDNSServiceSourceOptions{
						ServiceType: []corev1.ServiceType{
							corev1.ServiceTypeLoadBalancer,
							corev1.ServiceTypeClusterIP,
						},
					},
					LabelFilter: utils.MustParseLabelSelector("external-dns.mydomain.org/publish=yes"),
				},
				HostnameAnnotationPolicy: "Ignore",
				FQDNTemplate:             []string{fmt.Sprintf("{{.Name}}.%s", zoneDomain)},
			},
		},
	}
}

func routeExternalDNS(name, zoneID, zoneDomain, routerName string) operatorv1beta1.ExternalDNS {
	extDns := operatorv1beta1.ExternalDNS{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: operatorv1beta1.ExternalDNSSpec{
			Zones: []string{zoneID},
			Source: operatorv1beta1.ExternalDNSSource{
				ExternalDNSSourceUnion: operatorv1beta1.ExternalDNSSourceUnion{
					Type:        operatorv1beta1.SourceTypeRoute,
					LabelFilter: utils.MustParseLabelSelector("external-dns.mydomain.org/publish=yes"),
				},
				HostnameAnnotationPolicy: operatorv1beta1.HostnameAnnotationPolicyIgnore,
			},
		},
	}
	// this additional check can be removed with latest external-dns image (>v0.10.1)
	// instantiate the route additional information at ExternalDNS initiation level.
	if routerName != "" {
		extDns.Spec.Source.ExternalDNSSourceUnion.OpenShiftRoute = &operatorv1beta1.ExternalDNSOpenShiftRouteOptions{
			RouterName: routerName,
		}
	}
	return extDns
}

func routeExternalDNSV1Alpha1(name, zoneID, zoneDomain, routerName string) operatorv1alpha1.ExternalDNS {
	extDns := operatorv1alpha1.ExternalDNS{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: operatorv1alpha1.ExternalDNSSpec{
			Zones: []string{zoneID},
			Source: operatorv1alpha1.ExternalDNSSource{
				ExternalDNSSourceUnion: operatorv1alpha1.ExternalDNSSourceUnion{
					Type:        operatorv1alpha1.SourceTypeRoute,
					LabelFilter: utils.MustParseLabelSelector("external-dns.mydomain.org/publish=yes"),
				},
				HostnameAnnotationPolicy: operatorv1alpha1.HostnameAnnotationPolicyIgnore,
			},
		},
	}
	// this additional check can be removed with latest external-dns image (>v0.10.1)
	// instantiate the route additional information at ExternalDNS initiation level.
	if routerName != "" {
		extDns.Spec.Source.ExternalDNSSourceUnion.OpenShiftRoute = &operatorv1alpha1.ExternalDNSOpenShiftRouteOptions{
			RouterName: routerName,
		}
	}
	return extDns
}

func rootCredentials(kubeClient client.Client, name string) (map[string][]byte, error) {
	secret := &corev1.Secret{}
	secretName := types.NamespacedName{
		Name:      name,
		Namespace: "kube-system",
	}
	if err := kubeClient.Get(context.TODO(), secretName, secret); err != nil {
		return nil, fmt.Errorf("failed to get credentials secret %s: %w", secretName.Name, err)
	}
	return secret.Data, nil
}

// lookupCNAME retrieves the first canonical name of the given host.
// This function is different from net.LookupCNAME.
// net.LookupCNAME assumes the nameserver used is the recursive resolver (https://github.com/golang/go/blob/master/src/net/dnsclient_unix.go#L637).
// Therefore CNAME is tried to be resolved to its last canonical name, the quote from doc:
// "A canonical name is the final name after following zero or more CNAME records."
// This may be a problem if the default nameserver (from host /etc/resolv.conf, default lookup order is files,dns)
// is replaced (custom net.Resolver with overridden Dial function) with not recursive resolver
// and the other CNAMEs down to the last one are not known to this replaced nameserver.
// This may result in "no such host" error.
func lookupCNAME(host, server string) (string, error) {
	dnsClient := dns.Client{}
	message := &dns.Msg{}
	message.SetQuestion(dns.Fqdn(host), dns.TypeCNAME)
	r, _, err := dnsClient.Exchange(message, fmt.Sprintf("%s:53", server))
	if err != nil {
		return "", err
	}
	if len(r.Answer) == 0 {
		return "", fmt.Errorf("not found")
	}
	cname, ok := r.Answer[0].(*dns.CNAME)
	if !ok {
		return "", fmt.Errorf("not a CNAME record")
	}
	return cname.Target, nil
}

func lookupARecord(host, server string) ([]string, error) {
	dnsClient := &dns.Client{}
	message := &dns.Msg{}
	message.SetQuestion(dns.Fqdn(host), dns.TypeA)
	response, _, err := dnsClient.Exchange(message, fmt.Sprintf("%s:53", server))
	if err != nil {
		return nil, err
	}
	if len(response.Answer) == 0 {
		return nil, fmt.Errorf("not found")
	}
	var ips []string
	for _, ans := range response.Answer {
		if aRec, ok := ans.(*dns.A); ok {
			ips = append(ips, aRec.A.String())
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return ips, nil
}

func equalFQDN(name1, name2 string) bool {
	return dns.Fqdn(name1) == dns.Fqdn(name2)
}

func newHostNetworkController(name types.NamespacedName, domain string) *operatorv1.IngressController {
	return &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: name.Namespace,
			Name:      name.Name,
		},
		Spec: operatorv1.IngressControllerSpec{
			Domain:   domain,
			Replicas: pointer.Int32(1),
			EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.HostNetworkStrategyType,
			},
		},
	}
}

// enhancedIBClient provides enhancements not implemented in Infoblox golang client.
// https://pkg.go.dev/github.com/infobloxopen/infoblox-go-client
type enhancedIBClient struct {
	*ibclient.Connector
	httpClient *http.Client
}

func newEnhancedIBClient(hostConfig ibclient.HostConfig, transportConfig ibclient.TransportConfig) (*enhancedIBClient, error) {
	ibcli, err := ibclient.NewConnector(hostConfig, transportConfig, &ibclient.WapiRequestBuilder{}, &ibclient.WapiHttpRequestor{})
	if err != nil {
		return nil, err
	}

	return &enhancedIBClient{
		Connector: ibcli,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: !transportConfig.SslVerify,
				},
			},
		},
	}, nil
}

// addNameServer uses NIOS REST API to add the Grid host as the nameserver for the given DNS zone.
// Infoblox golang client has the interface for creating DNS zones.
// However these zones don't have NS/SOA records added by default.
func (c *enhancedIBClient) addNameServer(zoneRef, nameserver string) error {
	payload := fmt.Sprintf(`{"grid_primary":[{"name":"%s"}]}`, nameserver)
	qparams := map[string]string{
		"_return_fields+":   "fqdn,grid_primary",
		"_return_as_object": "1",
	}
	_, err := c.doHTTPRequest(context.TODO(), "PUT", "https://"+c.HostConfig.Host+"/wapi/v"+c.HostConfig.Version+"/"+zoneRef, qparams, []byte(payload))
	return err
}

// restartServices uses NIOS REST API to restart all Grid services.
// Some configurations don't take effect until the corresponding service is not restarted,
// see the doc: https://docs.infoblox.com/display/nios85/Configurations+Requiring+Service+Restart
func (c *enhancedIBClient) restartServices() error {
	respJSON, err := c.doHTTPRequest(context.TODO(), "GET", "https://"+c.HostConfig.Host+"/wapi/v"+c.HostConfig.Version+"/grid", nil, nil)
	if err != nil {
		return err
	}
	type Ref struct {
		Ref string `json:"_ref"`
	}
	resp := &[]Ref{}
	if err = json.Unmarshal(respJSON, resp); err != nil {
		return err
	}

	payload := `{"member_order" : "SIMULTANEOUSLY","service_option": "ALL"}`
	qparams := map[string]string{
		"_function": "restartservices",
	}
	_, err = c.doHTTPRequest(context.TODO(), "POST", "https://"+c.HostConfig.Host+"/wapi/v"+c.HostConfig.Version+"/"+(*resp)[0].Ref, qparams, []byte(payload))
	return err
}

func (c *enhancedIBClient) doHTTPRequest(ctx context.Context, method, url string, queryParams map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %s", err)
	}

	// set query parameters
	query := req.URL.Query()
	for k, v := range queryParams {
		query.Add(k, v)
	}
	req.URL.RawQuery = query.Encode()

	// set headers
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(c.HostConfig.Username, c.HostConfig.Password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %s", err)
	}
	defer resp.Body.Close()

	// 2xx
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("failure http status code received: %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read http response body: %s", err)
	}

	return respBody, nil
}

// readServerTLSCert returns PEM encoded TLS certificate of the given server.
func readServerTLSCert(addr string, selfSigned bool) ([]byte, error) {
	tlsConf := &tls.Config{
		InsecureSkipVerify: selfSigned,
	}

	conn, err := tls.Dial("tcp", addr, tlsConf)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	derCertsRaw := []byte{}
	certs := conn.ConnectionState().PeerCertificates
	for _, cert := range certs {
		derCertsRaw = append(derCertsRaw, cert.Raw...)
	}

	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derCertsRaw,
	}
	return pem.EncodeToMemory(block), nil
}

// ensureEnvVar ensures the environment variable is present in the given list.
func ensureEnvVar(vars []corev1.EnvVar, v corev1.EnvVar) []corev1.EnvVar {
	if vars == nil {
		return []corev1.EnvVar{v}
	}
	for i := range vars {
		if vars[i].Name == v.Name {
			vars[i].Value = v.Value
			return vars
		}
	}
	return append(vars, v)
}

// dnsQueryClusterInternal queries for a DNS hostname inside the VPC of a cluster using a dig pod. It returns a
// map structure of the resolved IPs for easy lookup.
func dnsQueryClusterInternal(ctx context.Context, t *testing.T, cl client.Client, cs *kubernetes.Clientset, namespace, hostname string) (map[string]struct{}, error) {
	t.Helper()

	clientPod := buildDigPod("digpod", namespace, hostname)
	if err := cl.Create(ctx, clientPod); err != nil {
		return nil, fmt.Errorf("failed to create pod %s/%s: %v", clientPod.Namespace, clientPod.Name, err)
	}
	defer func() {
		_ = cl.Delete(ctx, clientPod)
	}()

	// Loop until dig pod starts, then parse logs for query results.
	var responseCode string
	var gotIPs map[string]struct{}
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := cl.Get(ctx, types.NamespacedName{Name: clientPod.Name, Namespace: clientPod.Namespace}, clientPod); err != nil {
			t.Errorf("Failed to get pod %s/%s: %v", clientPod.Namespace, clientPod.Name, err)
			return false, nil
		}
		switch clientPod.Status.Phase {
		case corev1.PodRunning:
			t.Log("Waiting for dig pod to finish")
			return false, nil
		case corev1.PodPending:
			t.Log("Waiting for dig pod to start")
			return false, nil
		case corev1.PodFailed, corev1.PodSucceeded:
			// Failed or Succeeded, let's continue on to check the logs.
			break
		default:
			return true, fmt.Errorf("unhandled pod status type")
		}

		// Get logs of the dig pod.
		readCloser, err := cs.CoreV1().Pods(clientPod.Namespace).GetLogs(clientPod.Name, &corev1.PodLogOptions{
			Container: clientPod.Spec.Containers[0].Name,
			Follow:    false,
		}).Stream(ctx)
		if err != nil {
			t.Logf("Failed to read output from pod %s: %v (retrying)", clientPod.Name, err)
			return false, nil
		}
		scanner := bufio.NewScanner(readCloser)
		defer func() {
			if err := readCloser.Close(); err != nil {
				t.Fatalf("Failed to close reader for pod %s: %v", clientPod.Name, err)
			}
		}()

		gotIPs = make(map[string]struct{})
		for scanner.Scan() {
			line := scanner.Text()

			// Skip blank lines.
			if strings.TrimSpace(line) == "" {
				continue
			}
			// Parse status out (helpful for future debugging)
			if strings.HasPrefix(line, ";;") && strings.Contains(line, "status:") {
				responseCodeSection := strings.TrimSpace(strings.Split(line, ",")[1])
				responseCode = strings.Split(responseCodeSection, " ")[1]
				t.Logf("DNS Response Code: %s", responseCode)
			}
			// If it doesn't begin with ";", then we have an answer.
			if !strings.HasPrefix(line, ";") {
				splitAnswer := strings.Fields(line)
				if len(splitAnswer) < 5 {
					t.Logf("Expected dig answer to have 5 fields: %q", line)
					return true, nil
				}
				gotIP := strings.Fields(line)[4]
				gotIPs[gotIP] = struct{}{}
			}
		}
		t.Logf("Got IPs: %v", gotIPs)
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to observe the expected dig results: %v", err)
	}

	// Need to delete pod in case we start again.
	waitForDeletion(ctx, t, cl, clientPod, 5*time.Minute)

	return gotIPs, nil
}

// buildDigPod returns a pod definition for a pod with the given name and image
// and in the given namespace that digs the specified address.
func buildDigPod(name, namespace, address string, extraArgs ...string) *corev1.Pod {
	digArgs := []string{
		"+noall",
		"+answer",
		"+comments",
	}
	digArgs = append(digArgs, extraArgs...)
	digArgs = append(digArgs, address)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "dig",
					Image:   "openshift/origin-node",
					Command: []string{"/bin/dig"},
					Args:    digArgs,
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						Privileged:               pointer.Bool(false),
						RunAsNonRoot:             pointer.Bool(true),
						AllowPrivilegeEscalation: pointer.Bool(false),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
}

// getServiceIPs retrieves the provided service's IP or hostname and resolves the hostname to IPs (if applicable).
// Returns values are the serviceAddress (LoadBalancer IP or Hostname), the service resolved IPs, and an error
// (if applicable).
func getServiceIPs(ctx context.Context, t *testing.T, cl client.Client, timeout time.Duration, svcName types.NamespacedName) (string, map[string]struct{}, error) {
	t.Helper()

	// Get the IPs of the loadbalancer which is created for the service
	var serviceAddress string
	serviceResolvedIPs := make(map[string]struct{})
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		t.Log("Getting IPs of service's load balancer")
		var service corev1.Service
		err = cl.Get(ctx, svcName, &service)
		if err != nil {
			return false, err
		}

		// If there is no associated loadbalancer then retry later
		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return false, nil
		}

		// Get the IPs of the loadbalancer
		if service.Status.LoadBalancer.Ingress[0].IP != "" {
			serviceAddress = service.Status.LoadBalancer.Ingress[0].IP
			serviceResolvedIPs[service.Status.LoadBalancer.Ingress[0].IP] = struct{}{}
		} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			lbHostname := service.Status.LoadBalancer.Ingress[0].Hostname
			serviceAddress = lbHostname
			ips, err := lookupARecord(lbHostname, googleDNSServer)
			if err != nil {
				t.Logf("Waiting for IP of loadbalancer %s", lbHostname)
				// If the hostname cannot be resolved currently then retry later
				return false, nil
			}
			for _, ip := range ips {
				serviceResolvedIPs[ip] = struct{}{}
			}
		} else {
			t.Logf("Waiting for loadbalancer details for service %s", svcName.Name)
			return false, nil
		}
		t.Logf("Loadbalancer's IP(s): %v", serviceResolvedIPs)
		return true, nil
	}); err != nil {
		return "", nil, fmt.Errorf("failed to get loadbalancer IPs for service %s/%s: %v", svcName.Name, svcName.Namespace, err)
	}

	return serviceAddress, serviceResolvedIPs, nil
}

// waitForDeletion deletes an object and waits for it to be no longer found.
func waitForDeletion(ctx context.Context, t *testing.T, cl client.Client, obj client.Object, timeout time.Duration) {
	t.Helper()
	deletionPolicy := metav1.DeletePropagationForeground
	_ = wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		err := cl.Delete(ctx, obj, &client.DeleteOptions{PropagationPolicy: &deletionPolicy})
		if err != nil && !errors.IsNotFound(err) {
			t.Logf("failed to delete resource %s/%s: %v", obj.GetNamespace(), obj.GetName(), err)
			return false, nil
		} else if err == nil {
			t.Logf("retrying deletion of resource %s/%s", obj.GetNamespace(), obj.GetName())
			return false, nil
		}
		t.Logf("deleted resource %s/%s", obj.GetNamespace(), obj.GetName())
		return true, nil
	})
}
