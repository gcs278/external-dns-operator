//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	operatorv1alpha1 "github.com/openshift/external-dns-operator/api/v1alpha1"
	operatorv1beta1 "github.com/openshift/external-dns-operator/api/v1beta1"
	"github.com/openshift/external-dns-operator/pkg/version"

	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
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
	infobloxDNSProvider        = "INFOBLOX"
	dnsProviderEnvVar          = "DNS_PROVIDER"
	e2eSkipDNSProvidersEnvVar  = "E2E_SKIP_DNS_PROVIDERS"
	e2eSeparateOperandNsEnvVar = "E2E_SEPARATE_OPERAND_NAMESPACE"
)

var (
	kubeClient       client.Client
	kubeClientSet    *kubernetes.Clientset
	scheme           *runtime.Scheme
	nameServers      []string
	hostedZoneID     string
	helper           providerTestHelper
	hostedZoneDomain = baseZoneDomain
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

func initProviderHelper(openshiftCI bool, platformType string) (providerTestHelper, error) {
	switch platformType {
	case string(configv1.AWSPlatformType):
		return newAWSHelper(openshiftCI, kubeClient)
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

	if helper, err = initProviderHelper(openshiftCI, platformType); err != nil {
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

	// Get the resolved service IPs of the load balancer
	_, serviceIPs, err := getServiceIPs(context.TODO(), t, kubeClient, dnsPollingTimeout, types.NamespacedName{Name: testServiceName, Namespace: testNamespace})
	if err != nil {
		t.Fatalf("failed to get service IPs %s/%s: %v", testNamespace, testServiceName, err)
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

	// Get the resolved service IPs of the load balancer
	_, serviceIPs, err := getServiceIPs(context.TODO(), t, kubeClient, dnsPollingTimeout, types.NamespacedName{Name: testService, Namespace: testNamespace})
	if err != nil {
		t.Fatalf("failed to get service IPs %s/%s: %v", testNamespace, testServiceName, err)
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

// TestExternalDNSAssumeRole tests the assumeRole functionality in which you can specify a Role ARN to use another
// account's hosted zone for creating DNS records. Only AWS is supported.
func TestExternalDNSAssumeRole(t *testing.T) {
	// Only run this test if the DNS config contains the privateZoneIAMRole which indicates it's a "Shared VPC" cluster.
	// Note: Only AWS supports privateZoneIAMRole.
	dnsConfig := configv1.DNS{}
	err := kubeClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, &dnsConfig)
	if err != nil {
		t.Errorf("Failed to get dns 'cluster': %v\n", err)
	}
	if dnsConfig.Spec.Platform.AWS == nil || dnsConfig.Spec.Platform.AWS.PrivateZoneIAMRole == "" {
		t.Skipf("Test skipped on non-shared-VPC cluster")
	}

	t.Log("Ensuring test namespace")
	err = kubeClient.Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}})
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to ensure namespace %s: %v", testNamespace, err)
	}

	// Use an empty secret in our ExternalDNS object because:
	// 1. This E2E runs only on OpenShift and AWS, we will have a CredentialsRequest to provide credentials.
	// 2. To also validate the v1beta1 "workaround" of providing an empty credentials name with assumeRole due to
	// credentials being required by the CRD. Empty credentials should cause ExternalDNS Operator to use
	// CredentialsRequest.
	emptySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ""}}

	// Create an External object that uses the role ARN in the dns config to create DNS records in the private DNS
	// zone in another AWS account route 53.
	t.Log("Creating ExternalDNS object that assumes role our of private zone in another account's route 53")
	extDNS := helper.buildExternalDNS(testExtDNSName, dnsConfig.Spec.PrivateZone.ID, dnsConfig.Spec.BaseDomain, emptySecret)
	extDNS.Spec.Provider.AWS.AssumeRole = &operatorv1beta1.ExternalDNSAWSAssumeRoleOptions{
		ARN: dnsConfig.Spec.Platform.AWS.PrivateZoneIAMRole,
	}
	if err := kubeClient.Create(context.TODO(), &extDNS); err != nil {
		t.Fatalf("Failed to create external DNS %q: %v", testExtDNSName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), &extDNS)
	}()

	// Create a service of type LoadBalancer with the annotation targeted by the ExternalDNS resource.
	t.Log("Creating source service")
	service := defaultService(testServiceName, testNamespace)
	if err := kubeClient.Create(context.TODO(), service); err != nil {
		t.Fatalf("Failed to create test service %s/%s: %v", testNamespace, testServiceName, err)
	}
	defer func() {
		_ = kubeClient.Delete(context.TODO(), service)
	}()

	// Get the service address (hostname or IP) and the resolved service IPs of the load balancer.
	serviceAddress, serviceIPs, err := getServiceIPs(context.TODO(), t, kubeClient, dnsPollingTimeout, types.NamespacedName{Name: testServiceName, Namespace: testNamespace})
	if err != nil {
		t.Fatalf("failed to get service IPs %s/%s: %v", testNamespace, testServiceName, err)
	}

	// Query Route 53 API with assume role ARN from the dns config, then compare the results to ensure it matches the
	// service hostname.
	t.Logf("Querying Route 53 API to confirm DNS record exists in a different AWS account")
	expectedHost := fmt.Sprintf("%s.%s", testServiceName, dnsConfig.Spec.BaseDomain)
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		recordValues, err := helper.getDNSRecordValuesWithAssumeRole(dnsConfig.Spec.Platform.AWS.PrivateZoneIAMRole, dnsConfig.Spec.PrivateZone.ID, expectedHost, "A")
		if err != nil {
			t.Logf("Failed to get DNS record for shared VPC zone: %v", err)
			return false, nil
		} else if len(recordValues) == 0 {
			t.Logf("No DNS records with name %q", expectedHost)
			return false, nil
		}

		if _, found := recordValues[serviceAddress]; !found {
			if _, foundWithDot := recordValues[serviceAddress+"."]; !foundWithDot {
				t.Logf("DNS record with name %q didn't contain expected service IP %q", expectedHost, serviceAddress)
				return false, nil
			}
		}
		t.Logf("DNS record with name %q found in shared Route 53 private zone %q and matched service IPs", expectedHost, dnsConfig.Spec.PrivateZone.ID)
		return true, nil
	}); err != nil {
		t.Fatalf("Failed to verify that DNS has been correctly set.")
	}

	t.Logf("Querying for DNS record inside cluster VPC using a dig pod")

	// Verify that the IPs of the record created by ExternalDNS match the IPs of load balancer obtained in the previous
	// step. We will start a pod that runs a dig command for the expected hostname and parse the dig output.
	if err := wait.PollUntilContextTimeout(context.TODO(), dnsPollingInterval, dnsPollingTimeout, true, func(ctx context.Context) (done bool, err error) {
		gotIPs, err := dnsQueryClusterInternal(ctx, t, kubeClient, kubeClientSet, testNamespace, expectedHost)
		if err != nil {
			t.Errorf("Failed to query hostname inside cluster: %v", err)
			return false, nil
		}

		// If all IPs of the loadbalancer are not present, query again.
		if len(gotIPs) == 0 {
			return false, nil
		}
		if len(gotIPs) < len(serviceIPs) {
			return false, nil
		}
		// All expected IPs should be in the received IPs,
		// but these 2 sets are not necessary equal.
		for ip := range serviceIPs {
			if _, found := gotIPs[ip]; !found {
				return false, nil
			}
		}
		t.Log("Expected IPs are equal to IPs resolved.")
		return true, nil
	}); err != nil {
		t.Logf("Failed to verify that DNS has been correctly set.")
	} else {
		return
	}
	t.Fatalf("All nameservers failed to verify that DNS has been correctly set.")
}

// HELPER FUNCTIONS

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
