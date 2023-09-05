//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	operatorv1alpha1 "github.com/openshift/external-dns-operator/api/v1alpha1"
	operatorv1beta1 "github.com/openshift/external-dns-operator/api/v1beta1"
	"github.com/openshift/external-dns-operator/pkg/version"
)

const (
	baseZoneDomain             = "example-test.info"
	testNamespace              = "external-dns-test"
	testServiceName            = "test-service"
	testRouteName              = "test-route"
	testExtDNSName             = "test-extdns"
	operandNamespace           = "external-dns"
	operatorNamespace          = "external-dns-operator"
	rbacRsrcName               = "external-dns-operator"
	operatorServiceAccount     = "external-dns-operator"
	dnsPollingInterval         = 15 * time.Second
	dnsPollingTimeout          = 3 * time.Minute
	googleDNSServer            = "8.8.8.8"
	infobloxDNSProvider        = "INFOBLOX"
	dnsProviderEnvVar          = "DNS_PROVIDER"
	e2eSkipDNSProvidersEnvVar  = "E2E_SKIP_DNS_PROVIDERS"
	e2eSeparateOperandNsEnvVar = "E2E_SEPARATE_OPERAND_NAMESPACE"
)

var (
	kubeClient         client.Client
	kubeClientSet      *kubernetes.Clientset
	scheme             *runtime.Scheme
	nameServers        []string
	hostedZoneID       string
	helper             providerTestHelper
	hostedZoneDomain   = baseZoneDomain
	privateZoneIAMRole string
)

func init() {
	scheme = kscheme.Scheme
	if err := configv1.Install(scheme); err != nil {
		panic(err)
	}
	if err := operatorv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := operatorv1beta1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := operatorv1.Install(scheme); err != nil {
		panic(err)
	}
	if err := routev1.Install(scheme); err != nil {
		panic(err)
	}
	if err := olmv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}

func initKubeClient() error {
	kubeConfig, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get kube config: %w", err)
	}

	kubeClient, err = client.New(kubeConfig, client.Options{})
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	kubeClientSet, err = kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		fmt.Printf("failed to create kube clientset: %s\n", err)
		os.Exit(1)
	}

	return nil
}

func initProviderHelper(openshiftCI bool, platformType, privateZoneIAMRole string) (providerTestHelper, error) {
	switch platformType {
	case string(configv1.AWSPlatformType):
		return newAWSHelper(openshiftCI, kubeClient, privateZoneIAMRole)
	case string(configv1.AzurePlatformType):
		return newAzureHelper(kubeClient)
	case string(configv1.GCPPlatformType):
		return newGCPHelper(openshiftCI, kubeClient)
	case infobloxDNSProvider:
		return newInfobloxHelper(kubeClient)
	default:
		return nil, fmt.Errorf("unsupported provider: %q", platformType)
	}
}

func TestMain(m *testing.M) {
	var (
		err          error
		platformType string
		openshiftCI  bool
	)
	if err = initKubeClient(); err != nil {
		fmt.Printf("Failed to init kube client: %v\n", err)
		os.Exit(1)
	}

	if os.Getenv("OPENSHIFT_CI") != "" {
		openshiftCI = true
		if dnsProvider := os.Getenv(dnsProviderEnvVar); dnsProvider != "" {
			platformType = dnsProvider
		} else {
			platformType, err = getPlatformType(kubeClient)
			if err != nil {
				fmt.Printf("Failed to determine platform type: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		platformType = mustGetEnv(dnsProviderEnvVar)
	}

	if providersToSkip := os.Getenv(e2eSkipDNSProvidersEnvVar); len(providersToSkip) > 0 {
		for _, provider := range strings.Split(providersToSkip, ",") {
			if strings.EqualFold(provider, platformType) {
				fmt.Printf("Skipping e2e test for the provider %q!\n", provider)
				os.Exit(0)
			}
		}
	}

	if version.SHORTCOMMIT != "" {
		hostedZoneDomain = strconv.FormatInt(time.Now().Unix(), 10) + "." + version.SHORTCOMMIT + "." + baseZoneDomain
	}

	dnsConfig := configv1.DNS{}
	err = kubeClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, &dnsConfig)
	if err != nil {
		fmt.Printf("failed to get dns 'cluster': %v\n", err)
		os.Exit(1)
	}
	privateZoneIAMRole = dnsConfig.Spec.Platform.AWS.PrivateZoneIAMRole

	if helper, err = initProviderHelper(openshiftCI, platformType, privateZoneIAMRole); err != nil {
		fmt.Printf("Failed to init provider helper: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Ensuring hosted zone: %s\n", hostedZoneDomain)
	hostedZoneID, nameServers, err = helper.ensureHostedZone(hostedZoneDomain)
	if err != nil {
		fmt.Printf("Failed to created hosted zone for domain %s: %v\n", hostedZoneDomain, err)
		os.Exit(1)
	}

	if err := ensureOperandResources(); err != nil {
		fmt.Printf("Failed to ensure operand resources: %v\n", err)
	}

	exitStatus := m.Run()

	fmt.Printf("Deleting hosted zone: %s\n", hostedZoneDomain)
	err = helper.deleteHostedZone(hostedZoneID, hostedZoneDomain)
	if err != nil {
		fmt.Printf("Failed to delete hosted zone %s: %v\n", hostedZoneID, err)
		os.Exit(1)
	}
	os.Exit(exitStatus)
}

func TestOperatorAvailable(t *testing.T) {
	expected := []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue},
	}
	if err := waitForOperatorDeploymentStatusCondition(context.TODO(), t, kubeClient, expected...); err != nil {
		t.Errorf("Did not get expected available condition: %v", err)
	}
}

func TestExternalDNSWithRoute(t *testing.T) {
	t.Log("Ensuring test namespace")
	err := kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	// secret is needed only for DNS providers which cannot get their credentials from CCO
	// namely Infobox, BlueCat
	t.Log("Creating credentials secret")
	credSecret := helper.makeCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	t.Log("Creating external dns instance with source type route")
	extDNS := helper.buildOpenShiftExternalDNS(testExtDNSName, hostedZoneID, hostedZoneDomain, "", credSecret)
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// create a route with the annotation targeted by the ExternalDNS resource
	t.Log("Creating source route")
	testRouteHost := "myroute." + hostedZoneDomain
	route := testRoute(testRouteName, testNamespace, testRouteHost, testServiceName)
	if err := kubeClient.Create(context.TODO(), route); err != nil {
		t.Fatalf("Failed to create test route %s/%s: %v", testNamespace, testRouteName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), route)
	}()
	t.Logf("Created Route Host is %v", testRouteHost)

	// get the router canonical name
	var targetRoute routev1.Route
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		t.Log("Waiting for the route to be acknowledged by the router")
		err = kubeClient.Get(ctx, types.NamespacedName{
			Namespace: testNamespace,
			Name:      testRouteName,
		}, &targetRoute)
		if err != nil {
			return false, err
		}

		// if the status ingress slice is not populated by the ingress controller, try later
		if len(targetRoute.Status.Ingress) < 1 {
			return false, nil
		}
		return true, nil
	}); err != nil {
		t.Fatalf("Failed to retrieve the created route %s/%s: %v", testNamespace, testRouteName, err)
	}

	t.Logf("Target route ingress is %v", targetRoute.Status.Ingress)

	targetRouterCName := targetRoute.Status.Ingress[0].RouterCanonicalHostname
	if targetRouterCName == "" {
		t.Fatalf("Router's canonical name is empty %v", err)
	}
	t.Logf("Target router's CNAME is %v", targetRouterCName)

	// try all nameservers and fail only if all failed
	for _, nameSrv := range nameServers {
		t.Logf("Looking for DNS record in nameserver: %s", nameSrv)

		// verify dns records has been created for the route host.
		if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
			cNameHost, err := lookupCNAME(testRouteHost, nameSrv)
			if err != nil {
				t.Logf("Waiting for DNS record: %s, error: %v", testRouteHost, err)
				return false, nil
			}
			if equalFQDN(cNameHost, targetRouterCName) {
				t.Log("DNS record found")
				return true, nil
			}
			return false, nil
		}); err != nil {
			t.Logf("Failed to verify that DNS has been correctly set.")
		} else {
			return
		}
	}
	t.Fatalf("All nameservers failed to verify that DNS has been correctly set.")
}

func TestExternalDNSRecordLifecycle(t *testing.T) {
	t.Log("Ensuring test namespace")
	err := kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	t.Log("Creating credentials secret")
	credSecret := helper.makeCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	t.Log("Creating external dns instance")
	extDNS := helper.buildExternalDNS(testExtDNSName, hostedZoneID, hostedZoneDomain, credSecret)
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// create a service of type LoadBalancer with the annotation targeted by the ExternalDNS resource
	t.Log("Creating source service")
	service := defaultService(testServiceName, testNamespace)
	if err := kubeClient.Create(context.TODO(), service); err != nil {
		t.Fatalf("Failed to create test service %s/%s: %v", testNamespace, testServiceName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), service)
	}()

	serviceIPs, err := getServiceIP(t)
	if err != nil {
		t.Fatalf("Failed to get service IPs: %v", err)
	}

	// try all nameservers and fail only if all failed
	for _, nameSrv := range nameServers {
		t.Logf("Looking for DNS record in nameserver: %s", nameSrv)

		// verify that the IPs of the record created by ExternalDNS match the IPs of loadbalancer obtained in the previous step.
		if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
			expectedHost := fmt.Sprintf("%s.%s", testServiceName, hostedZoneDomain)
			ips, err := lookupARecord(expectedHost, nameSrv)
			if err != nil {
				t.Logf("Waiting for dns record: %s", expectedHost)
				return false, nil
			}
			gotIPs := make(map[string]struct{})
			for _, ip := range ips {
				gotIPs[ip] = struct{}{}
			}
			t.Logf("Got IPs: %v", gotIPs)

			// If all IPs of the loadbalancer are not present query again.
			if len(gotIPs) < len(serviceIPs) {
				return false, nil
			}
			// all expected IPs should be in the received IPs
			// but these 2 sets are not necessary equal
			for ip := range serviceIPs {
				if _, found := gotIPs[ip]; !found {
					return false, nil
				}
			}
			return true, nil
		}); err != nil {
			t.Logf("Failed to verify that DNS has been correctly set.")
		} else {
			return
		}
	}
	t.Fatalf("All nameservers failed to verify that DNS has been correctly set.")
}

// Test to verify the ExternalDNS should create the CNAME record for the OpenshiftRoute
// with multiple ingress controller deployed in Openshift.
// Route's host should resolve to the canonical name of the specified ingress controller.
func TestExternalDNSCustomIngress(t *testing.T) {
	testIngressNamespace := "test-extdns-openshift-route"
	t.Logf("Ensuring test namespace %s", testIngressNamespace)
	err := kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testIngressNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testIngressNamespace, err)
	}

	openshiftRouterName := "external-dns"
	// ingress controllers are supposed to be created in the ingress operator namespace
	name := types.NamespacedName{Namespace: "openshift-ingress-operator", Name: openshiftRouterName}
	ingDomain := fmt.Sprintf("%s.%s", name.Name, hostedZoneDomain)
	t.Log("Create custom ingress controller")
	ing := newHostNetworkController(name, ingDomain)
	if err = kubeClient.Create(context.TODO(), ing); err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to create ingresscontroller %s/%s: %v", name.Namespace, name.Name, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), ing)
	}()

	// secret is needed only for DNS providers which cannot get their credentials from CCO
	// namely Infobox, BlueCat
	t.Log("Creating credentials secret")
	credSecret := helper.makeCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	externalDnsServiceName := fmt.Sprintf("%s-source-as-openshift-route", testExtDNSName)
	t.Log("Creating external dns instance")
	extDNS := helper.buildOpenShiftExternalDNS(externalDnsServiceName, hostedZoneID, hostedZoneDomain, openshiftRouterName, credSecret)
	if err = kubeClient.Create(context.TODO(), &extDNS); err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	routeName := types.NamespacedName{Namespace: testIngressNamespace, Name: "external-dns-route"}
	host := fmt.Sprintf("app.%s", ingDomain)
	route := testRoute(routeName.Name, routeName.Namespace, host, testServiceName)
	t.Log("Creating test route")
	if err = kubeClient.Create(context.TODO(), route); err != nil {
		t.Fatalf("Failed to create route %s/%s: %v", routeName.Namespace, routeName.Name, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), route)
	}()

	canonicalName, err := fetchRouterCanonicalHostname(context.TODO(), t, routeName, ingDomain)
	if err != nil {
		t.Fatalf("Failed to get RouterCanonicalHostname for route %s/%s: %v", routeName.Namespace, routeName.Name, err)
	}
	t.Logf("CanonicalName: %s for the route: %s", canonicalName, routeName.Name)

	verifyCNAMERecordForOpenshiftRoute(context.TODO(), t, canonicalName, host)
}

func TestExternalDNSWithRouteV1Alpha1(t *testing.T) {
	t.Log("Ensuring test namespace")
	err := kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	// secret is needed only for DNS providers which cannot get their credentials from CCO
	// namely Infobox, BlueCat
	t.Log("Creating credentials secret")
	credSecret := helper.makeCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	t.Log("Creating external dns instance with source type route")
	extDNS := helper.buildOpenShiftExternalDNSV1Alpha1(testExtDNSName, hostedZoneID, hostedZoneDomain, "", credSecret)
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// create a route with the annotation targeted by the ExternalDNS resource
	t.Log("Creating source route")
	testRouteHost := "myroute." + hostedZoneDomain
	route := testRoute(testRouteName, testNamespace, testRouteHost, testServiceName)
	if err := kubeClient.Create(context.TODO(), route); err != nil {
		t.Fatalf("Failed to create test route %s/%s: %v", testNamespace, testRouteName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), route)
	}()
	t.Logf("Created Route Host is %v", testRouteHost)

	// get the router canonical name
	var targetRoute routev1.Route
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		t.Log("Waiting for the route to be acknowledged by the router")
		err = kubeClient.Get(ctx, types.NamespacedName{
			Namespace: testNamespace,
			Name:      testRouteName,
		}, &targetRoute)
		if err != nil {
			return false, err
		}

		// if the status ingress slice is not populated by the ingress controller, try later
		if len(targetRoute.Status.Ingress) < 1 {
			return false, nil
		}
		return true, nil
	}); err != nil {
		t.Fatalf("Failed to retrieve the created route %s/%s: %v", testNamespace, testRouteName, err)
	}

	t.Logf("Target route ingress is %v", targetRoute.Status.Ingress)

	targetRouterCName := targetRoute.Status.Ingress[0].RouterCanonicalHostname
	if targetRouterCName == "" {
		t.Fatalf("Router's canonical name is empty %v", err)
	}
	t.Logf("Target router's CNAME is %v", targetRouterCName)

	// try all nameservers and fail only if all failed
	for _, nameSrv := range nameServers {
		t.Logf("Looking for DNS record in nameserver: %s", nameSrv)

		// verify dns records has been created for the route host.
		if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
			cNameHost, err := lookupCNAME(testRouteHost, nameSrv)
			if err != nil {
				t.Logf("Waiting for DNS record: %s, error: %v", testRouteHost, err)
				return false, nil
			}
			if equalFQDN(cNameHost, targetRouterCName) {
				t.Log("DNS record found")
				return true, nil
			}
			return false, nil
		}); err != nil {
			t.Logf("Failed to verify that DNS has been correctly set.")
		} else {
			return
		}
	}
	t.Fatalf("All nameservers failed to verify that DNS has been correctly set.")
}

// TestExternalDNSSecretCredentialUpdate verifies that at first DNS records are not created when wrong secret is supplied.
// When the wrong secret is updated with the right values, DNS records are created.
func TestExternalDNSSecretCredentialUpdate(t *testing.T) {
	t.Log("Ensuring test namespace")
	testService := fmt.Sprintf("%s-credential-update", testServiceName)
	err := kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	t.Log("Creating wrong credentials secret")
	credSecret := makeWrongCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	t.Log("Creating external dns instance")
	extDNS := helper.buildExternalDNS(testExtDNSName, hostedZoneID, hostedZoneDomain, credSecret)
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// create a service of type LoadBalancer with the annotation targeted by the ExternalDNS resource
	t.Log("Creating source service")
	service := defaultService(testService, testNamespace)
	if err := kubeClient.Create(context.TODO(), service); err != nil {
		t.Fatalf("Failed to create test service %s/%s: %v", testNamespace, testService, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), service)
	}()

	serviceIPs := make(map[string]struct{})
	// get the IPs of the loadbalancer which is created for the service
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		t.Log("Getting IPs of service's load balancer")
		var service corev1.Service
		err = kubeClient.Get(ctx, types.NamespacedName{
			Namespace: testNamespace,
			Name:      testService,
		}, &service)
		if err != nil {
			return false, err
		}

		// if there is no associated loadbalancer then retry later
		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return false, nil
		}

		// get the IPs of the loadbalancer
		if service.Status.LoadBalancer.Ingress[0].IP != "" {
			serviceIPs[service.Status.LoadBalancer.Ingress[0].IP] = struct{}{}
		} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			lbHostname := service.Status.LoadBalancer.Ingress[0].Hostname
			ips, err := lookupARecord(lbHostname, googleDNSServer)
			if err != nil {
				t.Logf("Waiting for IP of loadbalancer %s", lbHostname)
				// if the hostname cannot be resolved currently then retry later
				return false, nil
			}
			for _, ip := range ips {
				serviceIPs[ip] = struct{}{}
			}
		} else {
			t.Logf("Waiting for loadbalancer details for service %s", testService)
			return false, nil
		}
		t.Logf("Loadbalancer's IP(s): %v", serviceIPs)
		return true, nil
	}); err != nil {
		t.Fatalf("Failed to get loadbalancer IPs for service %s/%s: %v", testNamespace, testService, err)
	}

	dnsCheck := make(chan bool)
	go func() {
		// try all nameservers and fail only if all failed
		for _, nameSrv := range nameServers {
			t.Logf("Looking for DNS record in nameserver: %s", nameSrv)
			// verify that the IPs of the record created by ExternalDNS match the IPs of loadbalancer obtained in the previous step.
			if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
				expectedHost := fmt.Sprintf("%s.%s", testService, hostedZoneDomain)
				ips, err := lookupARecord(expectedHost, nameSrv)
				if err != nil {
					t.Logf("Waiting for dns record: %s", expectedHost)
					return false, nil
				}
				gotIPs := make(map[string]struct{})
				for _, ip := range ips {
					gotIPs[ip] = struct{}{}
				}
				t.Logf("Got IPs: %v", gotIPs)

				// If all IPs of the loadbalancer are not present query again.
				if len(gotIPs) < len(serviceIPs) {
					return false, nil
				}
				// all expected IPs should be in the received IPs
				// but these 2 sets are not necessary equal
				for ip := range serviceIPs {
					if _, found := gotIPs[ip]; !found {
						return false, nil
					}
				}
				return true, nil
			}); err != nil {
				t.Logf("Failed to verify that DNS has been correctly set.")
			} else {
				dnsCheck <- true
				return
			}
		}
		dnsCheck <- false
	}()

	t.Logf("Updating credentials secret")
	credSecret.Data = helper.makeCredentialsSecret(operatorNamespace).Data
	err = kubeClient.Update(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to update credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}
	t.Logf("Credentials secret updated successfully")

	if resolved := <-dnsCheck; !resolved {
		t.Fatal("All nameservers failed to verify that DNS has been correctly set.")
	}
}

func TestExternalDNSAssumeRoleInSharedRoute53(t *testing.T) {
	dnsConfig := configv1.DNS{}
	err := kubeClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, &dnsConfig)
	if err != nil {
		t.Errorf("failed to get dns 'cluster': %v", err)
	}
	privateZoneIAMRole := dnsConfig.Spec.Platform.AWS.PrivateZoneIAMRole
	if privateZoneIAMRole == "" {
		t.Skipf("test skipped on non-shared-route53 cluster")
	}

	t.Log("Ensuring test namespace")
	err = kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	// secret is needed only for DNS providers which cannot get their credentials from CCO
	// namely Infobox, BlueCat
	t.Log("Creating credentials secret")
	credSecret := helper.makeCredentialsSecret(operatorNamespace)
	err = kubeClient.Create(context.TODO(), credSecret)
	if err != nil {
		t.Fatalf("Failed to create credentials secret %s/%s: %v", credSecret.Namespace, credSecret.Name, err)
	}

	extDNS := helper.buildExternalDNS(testExtDNSName, dnsConfig.Spec.PrivateZone.ID, dnsConfig.Spec.BaseDomain, credSecret)
	extDNS.Spec.Provider.AWS.AssumeRole = &operatorv1beta1.ExternalDNSAWSAssumeRoleOptions{
		ARN: &privateZoneIAMRole,
	}
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// create a service of type LoadBalancer with the annotation targeted by the ExternalDNS resource
	t.Log("Creating source service")
	service := defaultService(testServiceName, testNamespace)
	if err := kubeClient.Create(context.TODO(), service); err != nil {
		t.Fatalf("Failed to create test service %s/%s: %v", testNamespace, testServiceName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), service)
	}()

	serviceIPs, err := getServiceIP(t)
	if err != nil {
		t.Fatalf("Failed to get service IPs: %v", err)
	}

	expectedHost := fmt.Sprintf("%s.%s", testServiceName, dnsConfig.Spec.BaseDomain)
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		recordValues, err := helper.getDNSRecordValueInSharedVPCZone(dnsConfig.Spec.PrivateZone.ID, expectedHost, "A")
		if err != nil {
			t.Errorf("failed to get DNS record for shared VPC zone: %v", err)
			return false, nil
		} else if len(recordValues) == 0 {
			t.Errorf("no DNS records with name %q", expectedHost)
			return false, nil
		}
		// all expected IPs should be in the received IPs
		// but these 2 sets are not necessary equal
		for ip := range serviceIPs {
			if _, found := recordValues[ip]; !found {
				t.Errorf("DNS record with name %q didn't contain expected service IP %q", expectedHost, ip)
				return false, nil
			}
		}
		t.Logf("DNS record with name %q found in shared Route 53 private zone %q and matched service IPs", expectedHost, dnsConfig.Spec.PrivateZone.ID)
		return true, nil
	}); err != nil {
		t.Fatalf("failed to observe the DNS record properly configured in shared VPC zone: %v", err)
	}

	t.Logf("Looking for DNS record inside cluster VPC")

	// verify that the IPs of the record created by ExternalDNS match the IPs of loadbalancer obtained in the previous step.
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {

		clientPod := buildDigPod("digpod", testNamespace, expectedHost)
		if err := kubeClient.Create(context.TODO(), clientPod); err != nil {
			t.Fatalf("failed to create pod %s/%s: %v", clientPod.Namespace, clientPod.Name, err)
		}
		defer func() {
			_ = kubeClient.Delete(context.TODO(), clientPod)
		}()

		var responseCode string
		var gotIPs map[string]struct{}
		err = wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := kubeClient.Get(context.TODO(), types.NamespacedName{Name: clientPod.Name, Namespace: clientPod.Namespace}, clientPod); err != nil {
				t.Errorf("failed to get pod %s/%s: %v", clientPod.Namespace, clientPod.Name, err)
				return false, nil
			}
			switch clientPod.Status.Phase {
			case corev1.PodRunning:
				t.Log("waiting for pod to stop running")
				return false, nil
			case corev1.PodPending:
				t.Log("waiting for pod to start")
				return false, nil
			case corev1.PodFailed, corev1.PodSucceeded:
				break
			default:
				t.Fatalf("unhandled pod status type")
			}

			readCloser, err := kubeClientSet.CoreV1().Pods(clientPod.Namespace).GetLogs(clientPod.Name, &corev1.PodLogOptions{
				Container: clientPod.Spec.Containers[0].Name,
				Follow:    false,
			}).Stream(ctx)
			if err != nil {
				t.Logf("failed to read output from pod %s: %v (retrying)", clientPod.Name, err)
				return false, nil
			}
			scanner := bufio.NewScanner(readCloser)
			defer func() {
				if err := readCloser.Close(); err != nil {
					t.Fatalf("failed to close reader for pod %s: %v", clientPod.Name, err)
				}
			}()

			gotIPs = make(map[string]struct{})
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Println(line)

				// Skip blank lines
				if strings.TrimSpace(line) == "" {
					continue
				}
				// Parse status out
				if strings.HasPrefix(line, ";;") && strings.Contains(line, "status:") {
					responseCodeSection := strings.TrimSpace(strings.Split(line, ",")[1])
					responseCode = strings.Split(responseCodeSection, " ")[1]
				}
				// If it doesn't begin with ";", then we have an answer
				if !strings.HasPrefix(line, ";") {
					gotIP := strings.Fields(line)[4]
					gotIPs[gotIP] = struct{}{}
				}
			}
			return true, nil
		})
		if err != nil {
			t.Fatalf("failed to observe the expected log message: %v", err)
		}

		t.Logf("Got IPs: %v", gotIPs)
		t.Logf("DNS Status: %s", responseCode)

		waitForDeletion(context.TODO(), t, kubeClient, clientPod, 5*time.Minute)

		// If all IPs of the loadbalancer are not present query again.
		if len(gotIPs) == 0 {
			return false, nil
		}
		if len(gotIPs) < len(serviceIPs) {
			return false, nil
		}
		// all expected IPs should be in the received IPs
		// but these 2 sets are not necessary equal
		for ip := range serviceIPs {
			if _, found := gotIPs[ip]; !found {
				return false, nil
			}
		}
		t.Log("all IPs are equal")
		return true, nil
	}); err != nil {
		t.Logf("Failed to verify that DNS has been correctly set.")
	} else {
		return
	}
	t.Fatalf("All nameservers failed to verify that DNS has been correctly set.")

}

// HELPER FUNCTIONS

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
							Drop: []corev1.Capability{corev1.Capability("ALL")},
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

func getServiceIP(t *testing.T) (map[string]struct{}, error) {
	t.Helper()
	serviceIPs := make(map[string]struct{})
	// get the IPs of the loadbalancer which is created for the service
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		t.Log("Getting IPs of service's load balancer")
		var service corev1.Service
		err = kubeClient.Get(ctx, types.NamespacedName{
			Namespace: testNamespace,
			Name:      testServiceName,
		}, &service)
		if err != nil {
			return false, err
		}

		// if there is no associated loadbalancer then retry later
		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return false, nil
		}

		// get the IPs of the loadbalancer
		if service.Status.LoadBalancer.Ingress[0].IP != "" {
			serviceIPs[service.Status.LoadBalancer.Ingress[0].IP] = struct{}{}
		} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			lbHostname := service.Status.LoadBalancer.Ingress[0].Hostname
			ips, err := lookupARecord(lbHostname, googleDNSServer)
			if err != nil {
				t.Logf("Waiting for IP of loadbalancer %s", lbHostname)
				// if the hostname cannot be resolved currently then retry later
				return false, nil
			}
			for _, ip := range ips {
				serviceIPs[ip] = struct{}{}
			}
		} else {
			t.Logf("Waiting for loadbalancer details for service %s", testServiceName)
			return false, nil
		}
		t.Logf("Loadbalancer's IP(s): %v", serviceIPs)
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("Failed to get loadbalancer IPs for service %s/%s: %v", testNamespace, testServiceName, err)
	}

	return serviceIPs, nil
}

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

func ensureOperandResources() error {
	if os.Getenv(e2eSeparateOperandNsEnvVar) != "true" {
		return nil
	}

	if err := ensureOperandNamespace(); err != nil {
		return fmt.Errorf("Failed to create %s namespace: %v\n", operandNamespace, err)
	}

	if err := ensureOperandRole(); err != nil {
		return fmt.Errorf("Failed to create role external-dns-operator in ns %s: %v\n", operandNamespace, err)
	}

	if err := ensureOperandRoleBinding(); err != nil {
		return fmt.Errorf("Failed to create rolebinding external-dns-operator in ns %s: %v\n", operandNamespace, err)
	}

	return nil
}

func ensureOperandNamespace() error {
	return kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: operandNamespace}})
}

func ensureOperandRole() error {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"secrets", "serviceaccounts", "configmaps"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
		},
	}

	role := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacRsrcName,
			Namespace: operandNamespace,
		},
		Rules: rules,
	}
	return kubeClient.Create(context.TODO(), &role)
}

func ensureOperandRoleBinding() error {
	rb := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacRsrcName,
			Namespace: operandNamespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     rbacRsrcName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      operatorServiceAccount,
				Namespace: operatorNamespace,
			},
		},
	}
	return kubeClient.Create(context.TODO(), &rb)
}

func verifyCNAMERecordForOpenshiftRoute(ctx context.Context, t *testing.T, canonicalName, host string) {
	// try all nameservers and fail only if all failed
	recordExist := false
	for _, nameSrv := range nameServers {
		t.Logf("Looking for cname record in nameserver: %s", nameSrv)
		if err := wait.PollUntilContextTimeout(ctx, dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
			cname, err := lookupCNAME(host, nameSrv)
			if err != nil {
				t.Logf("Cname lookup failed for nameserver: %s , error: %v", nameSrv, err)
				return false, nil
			}
			if strings.Contains(cname, canonicalName) {
				recordExist = true
				return true, nil
			}
			return false, nil
		}); err != nil {
			t.Logf("Failed to verify host record with CNAME Record")
		}
	}

	if !recordExist {
		t.Fatalf("CNAME record not found in any name server")
	}
}

func fetchRouterCanonicalHostname(ctx context.Context, t *testing.T, routeName types.NamespacedName, routerDomain string) (string, error) {
	route := routev1.Route{}
	canonicalName := ""
	if err := wait.PollUntilContextTimeout(ctx, dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		err = kubeClient.Get(ctx, types.NamespacedName{
			Namespace: routeName.Namespace,
			Name:      routeName.Name,
		}, &route)
		if err != nil {
			return false, err
		}
		if len(route.Status.Ingress) < 1 {
			t.Logf("No ingress found in route, retrying..")
			return false, nil
		}

		for _, ingress := range route.Status.Ingress {
			if strings.Contains(ingress.RouterCanonicalHostname, routerDomain) {
				if ingressConditionHasStatus(ingress, routev1.RouteAdmitted, corev1.ConditionTrue) {
					canonicalName = ingress.RouterCanonicalHostname
					return true, nil
				}
				t.Logf("Router didn't admit the route, retrying..")
				return false, nil
			}
		}
		t.Logf("Unable to fetch the canonicalHostname, retrying..")
		return false, nil
	}); err != nil {
		return "", err
	}
	return canonicalName, nil
}

func ingressConditionHasStatus(ingress routev1.RouteIngress, condition routev1.RouteIngressConditionType, status corev1.ConditionStatus) bool {
	for _, c := range ingress.Conditions {
		if condition == c.Type {
			return c.Status == status
		}
	}
	return false
}

func makeWrongCredentialsSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("wrong-credentials-%s", randomString(16)),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"wrong_credentials": []byte("wrong_access"),
		},
	}
}
